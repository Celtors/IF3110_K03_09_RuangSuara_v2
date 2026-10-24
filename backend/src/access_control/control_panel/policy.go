package controlpanel

import (
	"backend/access_control/models"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type GroupPolicyPayload struct {
	GroupId uuid.UUID `json:"group_id" binding:"required"`
	Policy  string    `json:"policy" binding:"required"`
	Effect  string    `json:"effect"`
}

type GroupFromPolicy struct {
	GroupId     uuid.UUID `json:"group_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Effect      string    `json:"effect"`
	CreatedAt   time.Time `json:"created_at"`
}

type PolicyFromGroup struct {
	Policy    string    `json:"policy"`
	Effect    string    `json:"effect"`
	CreatedAt time.Time `json:"created_at"`
}

type PolicyTypePayload struct {
	Policy string `json:"policy"`
}

func (payload GroupPolicyPayload) GetDBStructCreate() models.GroupPolicy {
	return models.GroupPolicy{
		GroupId: payload.GroupId,
		Policy:  payload.Policy,
		Effect:  "Allow",
	}
}

func AddGroupPolicyRequest(c *gin.Context, db *gorm.DB) {
	var appGroupPayload GroupPolicyPayload

	if err := c.ShouldBindJSON(&appGroupPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to add policy: " + err.Error(),
		})
		return
	}

	policyModel := appGroupPayload.GetDBStructCreate()

	if err := db.Create(&policyModel).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to add policy: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status":  "success",
		"message": "Successfully added policy",
	})
}

func RemoveGroupPolicyRequest(c *gin.Context, db *gorm.DB) {
	var policyPayload GroupPolicyPayload

	if err := c.ShouldBindJSON(&policyPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to remove policy: " + err.Error(),
		})
		return
	}

	db.Where("group_id = ? AND policy = ?", policyPayload.GroupId, policyPayload.Policy).Delete(&models.GroupPolicy{})

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Successfully removed policy",
	})
}

func UpdateGroupPolicyRequest(c *gin.Context, db *gorm.DB) {
	var policyPayload GroupPolicyPayload

	if err := c.ShouldBindJSON(&policyPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to update policy: " + err.Error(),
		})
		return
	}

	db.Model(&models.GroupPolicy{}).
		Where("group_id = ? AND policy = ?", policyPayload.GroupId, policyPayload.Policy).
		Update("effect", policyPayload.Effect)

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Successfully updated policy",
	})
}

func GetPoliciesFromGroup(c *gin.Context, db *gorm.DB) {
	var policiesFromGroup []PolicyFromGroup

	var groupPKey PKeyPayload

	if err := c.ShouldBindJSON(&groupPKey); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to get groups: " + err.Error(),
		})
		return
	}

	db.Model(&models.GroupPolicy{}).
		Select("group_policies.policy, group_policies.effect, group_policies.created_at").
		Joins("JOIN groups_ ON groups_.id = group_policies.group_id").
		Where("group_id = ?", groupPKey.Id).
		Find(&policiesFromGroup)

	c.JSON(http.StatusOK, policiesFromGroup)
}

func GetGroupWithPolicy(c *gin.Context, db *gorm.DB) {
	var policiesFromGroup []GroupFromPolicy

	var policy PolicyTypePayload

	if err := c.ShouldBindJSON(&policy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to get apps: " + err.Error(),
		})
		return
	}

	db.Model(&models.GroupPolicy{}).
		Select("groups_.id, groups_.name, groups_.description, group_policies.effect, group_policies.created_at").
		Joins("JOIN groups_ ON groups_.id = group_policies.group_id").
		Where("policy = ?", policy.Policy).
		Find(&policiesFromGroup)

	c.JSON(http.StatusOK, policiesFromGroup)
}
