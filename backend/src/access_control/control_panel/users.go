package controlpanel

import (
	"backend/access_control/models"
	"backend/access_control/utility"
	"backend/internal/telemetry"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type CreateUserPayload struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required,gte=8"`
}

type UpdateUserPayload struct {
	Id       uuid.UUID `json:"id" binding:"required"`
	Username string    `json:"username"`
	Password string    `json:"password" binding:"omitempty,gte=8"`
	Status   string    `json:"status"`
}

type PKeyPayload struct {
	Id uuid.UUID `json:"id" binding:"required"`
}

func (payload CreateUserPayload) GetDBStruct() (models.User, error) {
	hashedPass, err := utility.HashPassword(payload.Password)
	return models.User{
		Username:     payload.Username,
		PasswordHash: hashedPass,
		Status:       "Active",
	}, err
}

func (payload UpdateUserPayload) GetDBStruct() (models.User, models.User, error) {
	var hashedPass string = ""
	var err error = nil
	if payload.Password != "" {
		hashedPass, err = utility.HashPassword(payload.Password)
	}

	return models.User{
			ID: payload.Id,
		},
		models.User{
			Username:     payload.Username,
			PasswordHash: hashedPass,
			Status:       payload.Status,
		}, err
}

func (payload PKeyPayload) GetUserModel() models.User {
	return models.User{ID: payload.Id}
}

func CreateUserRequest(c *gin.Context, db *gorm.DB) {
	var userPayload CreateUserPayload

	if err := c.ShouldBindJSON(&userPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to create user: " + err.Error(),
		})
		return
	}
	userModel, err := userPayload.GetDBStruct()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": "Failed to create user: " + err.Error(),
		})
		return
	}

	if err := db.Create(&userModel).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to create user: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status":  "success",
		"message": "User successfully created",
	})
}

func GetAllUsersRequest(c *gin.Context, db *gorm.DB) {
	var users []models.User

	if err := db.Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": "Failed to get user data: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, users)
}

func UpdateUserRequest(c *gin.Context, db *gorm.DB) {
	var userPayload UpdateUserPayload

	if err := c.ShouldBindJSON(&userPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to update user: " + err.Error(),
		})
		return
	}

	if userPayload.Status != "" && userPayload.Status != "Active" && userPayload.Status != "Inactive" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Status must be Active or Inactive"})
		return
	}
	userQuery, updatedUserModel, err := userPayload.GetDBStruct()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Could not hash the new password"})
		return
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&userQuery).Updates(updatedUserModel)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		revokeReason := ""
		if updatedUserModel.Status == "Inactive" {
			revokeReason = "account_deactivated"
		} else if updatedUserModel.PasswordHash != "" {
			revokeReason = "password_changed"
		}
		if revokeReason != "" {
			if err := models.RevokeUserSessions(tx, userQuery.ID, revokeReason); err != nil {
				return err
			}
		}
		if updatedUserModel.Status == "Inactive" {
			return tx.Where("leader_id = ?", userQuery.ID).Delete(&models.GroupLeader{}).Error
		}
		return nil
	})
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "User not found"})
		return
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Could not update the user"})
		return
	}
	if updatedUserModel.Status == "Inactive" {
		slog.LogAttrs(c.Request.Context(), slog.LevelInfo, "authentication.account_deactivated",
			slog.String("status", "succeeded"), telemetry.ID("affected_user_id", userQuery.ID.String()))
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "User successfully updated",
	})
}

func DeleteUserRequest(c *gin.Context, db *gorm.DB) {
	var userPKey PKeyPayload

	if err := c.ShouldBindJSON(&userPKey); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to delete user: " + err.Error(),
		})
		return
	}

	userModel := userPKey.GetUserModel()
	if err := db.Delete(&userModel).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to delete user: " + err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "User successfully deleted",
	})
}
