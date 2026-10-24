package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type User struct {
	ID           uuid.UUID `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Name         string            `json:"name"`
	Email        string            `json:"email"`
	PasswordHash string            `json:"password_hash"`
	Status       string            `json:"status"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

func (u User) IsActive() bool {
	return u.Status == "Active"
}

func (u User) AuthorizedGroupCount(policy string, db *gorm.DB) int {
	groups := db.Model(&GroupPolicy{}).Select("group_id").Where("policy = ? AND effect = ?", policy, "Blocked")

	var userGroup []UserGroup
	db.Model(&UserGroup{}).Where("user_id = ? AND group_id IN (?)", u.ID, groups).Find(&userGroup)

	return len(userGroup)
}

func (u User) IsAuthorized(policy string, db *gorm.DB) bool {
	groups := db.Model(&GroupPolicy{}).Select("group_id").Where("policy = ? AND effect = ?", policy, "Blocked")

	var userGroup []UserGroup
	db.Model(&UserGroup{}).Where("user_id = ? AND group_id IN (?)", u.ID, groups).Find(&userGroup)

	return (u.AuthorizedGroupCount(policy, db) > 0) && (len(userGroup) == 0)
}

func (u User) GetLatestActiveSession(db *gorm.DB) *uuid.UUID {
	var sessions []SSOSession
	db.Model(&SSOSession{}).Where("user_id = ? AND status = ?", u.ID, "Active").Find(&sessions)

	if len(sessions) <= 0 {
		return nil
	}

	return &(sessions[0].ID)
}

type Group struct {
	ID          uuid.UUID `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func (Group) TableName() string {
	return "groups_"
}

type UserGroup struct {
	ID        uuid.UUID `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserId    uuid.UUID `json:"user_id"`
	GroupId   uuid.UUID `json:"group_id"`
	CreatedAt time.Time         `json:"created_at"`
}

func (u User) GetGroupsUUID(db *gorm.DB) *[]uuid.UUID {
	var groups []UserGroup
	db.Where("user_id = ?", u.ID).Find(&groups)
	groupsUUID := make([]uuid.UUID, len(groups))
	for i := 0; i < len(groups); i++ {
		groupsUUID[i] = groups[i].GroupId
	}
	return &groupsUUID
}



type SSOSession struct {
	ID               uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserId           uuid.UUID
	SessionTokenHash string
	Status           string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	LastActivityAt   *time.Time
	RevokedAt        *time.Time
	RevokedReason    string
	IpAddress        string
	UserAgent        string
}

func (SSOSession) TableName() string {
	return "sso_sessions"
}

func (session SSOSession) IsValid() bool {
	return session.Status == "Active" && session.ExpiresAt.After(time.Now()) && (session.RevokedAt == nil)
}

func (session SSOSession) UpdateStatus(db *gorm.DB) {
	if session.ExpiresAt.Before(time.Now()) {
		db.Model(&SSOSession{}).Where("id = ?", session.ID).Updates(SSOSession{Status: "Expired"})
	}
}

func (session SSOSession) MarkActivity(db *gorm.DB) {
	t := time.Now()
	db.Model(&SSOSession{}).Where("id = ?", session.ID).Updates(SSOSession{LastActivityAt: &t})
}

type GroupPolicy struct {
	ID            uuid.UUID
	GroupId   	  uuid.UUID
	Policy 		  string
	Effect 		  string
	CreatedAt     time.Time
}

func (GroupPolicy) TableName() string {
	return "group_policies"
}
