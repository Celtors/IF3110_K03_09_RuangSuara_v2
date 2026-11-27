package controlpanel

import (
	"backend/access_control/models"
	"backend/internal/telemetry"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UserGroupPayload struct {
	UserId  uuid.UUID `json:"user_id" binding:"required"`
	GroupId uuid.UUID `json:"group_id" binding:"required"`
}

type UserFromGroupPayload struct {
	UserId   uuid.UUID `json:"user_id"`
	Username string    `json:"username"`
	Status   string    `json:"status"`
	PlacedAt time.Time `json:"placed_at"`
}

type GroupFromUserPayload struct {
	GroupId     uuid.UUID `json:"group_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	PlacedAt    time.Time `json:"placed_at"`
}

type GroupLeaderPayload struct {
	UserId   uuid.UUID `json:"user_id"`
	Username string    `json:"username"`
}

func (payload UserGroupPayload) GetDBStructCreate() models.UserGroup {
	return models.UserGroup{
		UserId:  payload.UserId,
		GroupId: payload.GroupId,
	}
}

func (payload UserGroupPayload) GetLeaderStruct() models.GroupLeader {
	return models.GroupLeader{
		LeaderId: payload.UserId,
		GroupId:  payload.GroupId,
	}
}

func AddUserToGroup(c *gin.Context, db *gorm.DB) {
	var userGroupPayload UserGroupPayload

	if err := c.ShouldBindJSON(&userGroupPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to add user to group: " + err.Error(),
		})
		return
	}

	userGroupModel := userGroupPayload.GetDBStructCreate()

	if err := db.Create(&userGroupModel).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to add user to group: " + err.Error(),
		})
		return
	}

	slog.LogAttrs(c.Request.Context(), slog.LevelInfo, "authorization.membership_added",
		slog.String("status", "succeeded"), telemetry.ID("group_id", userGroupPayload.GroupId.String()),
		telemetry.ID("affected_user_id", userGroupPayload.UserId.String()))
	c.JSON(http.StatusCreated, gin.H{
		"status":  "success",
		"message": "Successfully added user to group",
	})
}

func RemoveUserFromGroup(c *gin.Context, db *gorm.DB) {
	var userGroupPayload UserGroupPayload

	if err := c.ShouldBindJSON(&userGroupPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to remove user from group: " + err.Error(),
		})
		return
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("user_id = ? AND group_id = ?", userGroupPayload.UserId, userGroupPayload.GroupId).Delete(&models.UserGroup{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Where("leader_id = ? AND group_id = ?", userGroupPayload.UserId, userGroupPayload.GroupId).
			Delete(&models.GroupLeader{}).Error
	})
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "Group membership not found"})
		return
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Could not remove the group membership"})
		return
	}
	slog.LogAttrs(c.Request.Context(), slog.LevelInfo, "authorization.membership_removed",
		slog.String("status", "succeeded"), telemetry.ID("group_id", userGroupPayload.GroupId.String()),
		telemetry.ID("affected_user_id", userGroupPayload.UserId.String()))

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Successfully removed user from group",
	})
}

func GetUsersFromGroupRequest(c *gin.Context, db *gorm.DB) {
	var usersFromGroup []UserFromGroupPayload

	group_id, exists := c.GetQuery("id")

	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to get users from group",
		})
		return
	}

	db.Model(&models.UserGroup{}).
		Select("user_groups.user_id, users.username, users.status, user_groups.created_at AS placed_at").
		Joins("LEFT JOIN users ON users.id = user_groups.user_id").
		Where("group_id = ?", group_id).
		Find(&usersFromGroup)

	c.JSON(http.StatusOK, usersFromGroup)
}

func GetGroupsFromUserRequest(c *gin.Context, db *gorm.DB) {
	var groupFromUsers []GroupFromUserPayload

	user_id, exists := c.GetQuery("id")

	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to get groups from user",
		})
		return
	}

	db.Model(&models.UserGroup{}).
		Select("user_groups.group_id, groups_.name, groups_.description, user_groups.created_at AS placed_at").
		Joins("LEFT JOIN groups_ ON user_groups.group_id = groups_.id").
		Where("user_groups.user_id = ?", user_id).
		Find(&groupFromUsers)

	c.JSON(http.StatusOK, groupFromUsers)
}

func SetGroupLeader(c *gin.Context, db *gorm.DB) {
	var userGroupPayload UserGroupPayload

	if err := c.ShouldBindJSON(&userGroupPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to set leader to group: " + err.Error(),
		})
		return
	}

	leader := userGroupPayload.GetLeaderStruct()
	err := db.Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
			Take(&user, "id = ? AND status = ?", leader.LeaderId, "Active").Error; err != nil {
			return err
		}
		var member models.UserGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Take(&member, "user_id = ? AND group_id = ?", leader.LeaderId, leader.GroupId).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "group_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"leader_id"}),
		}).Create(&leader).Error
	})
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "The leader must be an active group member"})
		return
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Could not assign the group leader"})
		return
	}
	slog.LogAttrs(c.Request.Context(), slog.LevelInfo, "authorization.group_leader_assigned",
		slog.String("status", "succeeded"), telemetry.ID("group_id", leader.GroupId.String()),
		telemetry.ID("affected_user_id", leader.LeaderId.String()))
	c.JSON(http.StatusCreated, gin.H{
		"status":  "success",
		"message": "Successfully set user to be leader of group",
	})
}

func GetGroupLeader(c *gin.Context, db *gorm.DB) {
	groupID, err := uuid.Parse(c.Query("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "A valid group ID is required"})
		return
	}
	var leader GroupLeaderPayload
	err = db.Table("group_leaders gl").
		Select("gl.leader_id AS user_id, u.username").
		Joins("JOIN user_groups ug ON ug.group_id = gl.group_id AND ug.user_id = gl.leader_id").
		Joins("JOIN users u ON u.id = gl.leader_id AND u.status = 'Active'").
		Where("gl.group_id = ?", groupID).Take(&leader).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "Leader not found"})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Could not load the group leader"})
	default:
		c.JSON(http.StatusOK, leader)
	}
}
