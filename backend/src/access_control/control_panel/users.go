package controlpanel

import (
	"net/http"
	"backend/access_control/utility"
	"backend/access_control/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type CreateUserPayload struct {
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,gte=8"`
}

type UpdateUserPayload struct {
	Id       uuid.UUID `json:"id" binding:"required"`
	Name     string            `json:"name"`
	Email    string            `json:"email" binding:"omitempty,email"`
	Password string            `json:"password" binding:"omitempty,gte=8"`
	Status   string            `json:"status"`
}

type PKeyPayload struct {
	Id uuid.UUID `json:"id" binding:"required"`
}

func (payload CreateUserPayload) GetDBStruct() (models.User, error) {
	hashedPass, err := utility.HashPassword(payload.Password)
	return models.User{
		Name:         payload.Name,
		Email:        payload.Email,
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
			Name:         payload.Name,
			Email:        payload.Email,
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
	
	
	userQuery, updatedUserModel, err := userPayload.GetDBStruct()

	if err != nil {
		return
	}

	if err := db.Model(&userQuery).Updates(updatedUserModel).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to update user: " + err.Error(),
		})
		return
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