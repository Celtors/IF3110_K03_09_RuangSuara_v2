package controlpanel

import (
	"backend/access_control/models"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type CreateGroupPayload struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

type UpdateGroupPayload struct {
	Id          uuid.UUID `json:"id" binding:"required"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
}

type GroupCompact struct {
	Id          uuid.UUID `json:"id"`
	Name        string            `json:"name"`
}

type GroupViewPayload struct {
	Id          uuid.UUID `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	LeaderName  string            `json:"leader_name"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (payload CreateGroupPayload) GetDBStruct() (models.Group) {
	return models.Group{
		Name:        payload.Name,
		Description: payload.Description,
	}
}

func (payload UpdateGroupPayload) GetDBStruct() (models.Group, models.Group) {
	return models.Group{
			ID: payload.Id,
		},
		models.Group{
			Name:        payload.Name,
			Description: payload.Description,
		}
}

func (payload PKeyPayload) GetGroupModel() models.Group {
	return models.Group{ID: payload.Id}
}

func CreateGroupRequest(c *gin.Context, db *gorm.DB) {
	var groupPayload CreateGroupPayload

	if err := c.ShouldBindJSON(&groupPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to create group: " + err.Error(),
		})
		return
	}

	groupModel := groupPayload.GetDBStruct()


	if err := db.Create(&groupModel).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to create group: " + err.Error(),
		})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"status":  "success",
		"message": "Group successfully created",
	})
}

func GetAllGroupsRequest(c *gin.Context, db *gorm.DB) {
    var groups []GroupViewPayload

    err := db.Table("groups_ AS g").
        Select(`
            g.id,
            g.name,
            g.description,
            COALESCE(u.username, '') AS leader_name,
            g.created_at,
            g.updated_at
        `).
        Joins("LEFT JOIN group_leaders gl ON g.id = gl.group_id").
        Joins("LEFT JOIN users u ON u.id = gl.leader_id").
        Scan(&groups).Error

    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{
            "status":  "error",
            "message": "Failed to get group data: " + err.Error(),
        })
        return
    }

    c.JSON(http.StatusOK, groups)
}



func UpdateGroupRequest(c *gin.Context, db *gorm.DB) {
	var groupPayload UpdateGroupPayload

	if err := c.ShouldBindJSON(&groupPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to update group: " + err.Error(),
		})
		return
	}

	userGroup, updatedGroupModel := groupPayload.GetDBStruct()

	if err := db.Model(&userGroup).Updates(updatedGroupModel).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to update group: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Group successfully updated",
	})
}

func DeleteGroupRequest(c *gin.Context, db *gorm.DB) {
	var groupPKey PKeyPayload

	if err := c.ShouldBindJSON(&groupPKey); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to delete group: " + err.Error(),
		})
		return
	}

	groupModel := groupPKey.GetGroupModel()
	if err := db.Delete(&groupModel).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to delete group: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Group successfully deleted",
	})
}