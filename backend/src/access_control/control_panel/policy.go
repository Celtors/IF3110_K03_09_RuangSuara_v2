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
	GroupId    uuid.UUID `json:"group_id" binding:"required"`
	DatabaseId uuid.UUID `json:"database_id" binding:"required"`
	Effect     string    `json:"effect"`
}

type GroupFromDatabasePolicy struct {
	GroupId     uuid.UUID `json:"group_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Effect      string    `json:"effect"`
	CreatedAt   time.Time `json:"created_at"`
}

type DatabaseFromGroupPolicy struct {
	DatabaseId uuid.UUID `json:"database_id"`

	Effect    string    `json:"effect"`
	CreatedAt time.Time `json:"created_at"`
}

func (payload GroupPolicyPayload) GetDBStructCreate() models.GroupPolicy {
	return models.GroupPolicy{
		GroupId:    payload.GroupId,
		DatabaseId: payload.DatabaseId,
		Effect:     "ALLOW",
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

	if appGroupPayload.Effect != "" && appGroupPayload.Effect != "ALLOW" && appGroupPayload.Effect != "DENY" && appGroupPayload.Effect != "Allow" && appGroupPayload.Effect != "Blocked" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Invalid policy effect"})
		return
	}
	policyModel := appGroupPayload.GetDBStructCreate()
	if appGroupPayload.Effect == "DENY" || appGroupPayload.Effect == "Blocked" {
		policyModel.Effect = "DENY"
	}

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

	db.Where("group_id = ? AND database_id = ?", policyPayload.GroupId, policyPayload.DatabaseId).Delete(&models.GroupPolicy{})

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

	effect := policyPayload.Effect
	if effect == "Blocked" {
		effect = "DENY"
	}
	if effect == "Allow" {
		effect = "ALLOW"
	}
	if effect != "ALLOW" && effect != "DENY" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Invalid policy effect"})
		return
	}
	result := db.Model(&models.GroupPolicy{}).Where("group_id = ? AND database_id = ?", policyPayload.GroupId, policyPayload.DatabaseId).Update("effect", effect)
	if result.Error != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Failed to update policy"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "Policy not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Successfully updated policy",
	})
}

func GetPoliciesRequest(c *gin.Context, db *gorm.DB) {
	group_id := c.DefaultQuery("groupId", "")
	database_id := c.DefaultQuery("databaseId", "")

	if group_id != "" && database_id != "" {
		var groupPolicies []models.GroupPolicy

		db.Model(&models.GroupPolicy{}).
			Joins("INNER JOIN groups_ ON groups_.id = group_policies.group_id").
			Joins("INNER JOIN databases ON databases.id = group_policies.database_id").
			Where("group_id = ? AND database_id = ?", group_id, database_id).
			Find(&groupPolicies)

		c.JSON(http.StatusOK, groupPolicies)
	} else if group_id != "" {
		var databasesFromGroup []DatabaseFromGroupPolicy

		db.Model(&models.GroupPolicy{}).
			Select("databases.id, databases.name, group_policies.effect, group_policies.created_at").
			Joins("INNER JOIN databases ON databases.id = group_policies.database_id").
			Where("group_id = ?", group_id).
			Find(&databasesFromGroup)

		c.JSON(http.StatusOK, databasesFromGroup)
	} else if database_id != "" {
		var groupFromDatabase []GroupFromDatabasePolicy

		db.Model(&models.GroupPolicy{}).
			Select("groups_.id, groups_.name, groups_.description, group_policies.effect, group_policies.created_at").
			Joins("INNER JOIN groups_ ON groups_.id = group_policies.group_id").
			Where("database_id = ?", database_id).
			Find(&groupFromDatabase)

		c.JSON(http.StatusOK, groupFromDatabase)
	} else {
		var policies []models.GroupPolicy

		if err := db.Find(&policies).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "Failed to get user data: " + err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, policies)
	}
}
