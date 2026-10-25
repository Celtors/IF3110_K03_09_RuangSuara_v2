package databasemanagement

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Database struct {
	ID                 uuid.UUID `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	DBMS               string    `json:"dbms" gorm:"column:dbms;not null;default:postgres;size:16"`
	Name               string    `json:"name" gorm:"not null;size:100"`
	Host               string    `json:"host" gorm:"not null;size:253"`
	Port               int       `json:"port" gorm:"not null"`
	DatabaseName       string    `json:"database_name" gorm:"column:database_name;not null;size:128"`
	Username           string    `json:"username" gorm:"not null;size:128"`
	PasswordCiphertext string    `json:"-" gorm:"column:password_ciphertext;not null;type:text"`
	SSLMode            string    `json:"ssl_mode" gorm:"not null;default:verify-full;size:32"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (Database) TableName() string { return "databases" }

type GroupPolicy struct {
	ID         uuid.UUID `json:"id" gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	GroupID    uuid.UUID `json:"group_id" gorm:"column:group_id;type:uuid;not null;uniqueIndex:idx_group_database_policy"`
	DatabaseID uuid.UUID `json:"database_id" gorm:"column:database_id;type:uuid;not null;uniqueIndex:idx_group_database_policy"`
	Effect     string    `json:"effect" gorm:"not null;size:10;default:ViewOnly"`
	CreatedAt  time.Time `json:"created_at"`
}

func (GroupPolicy) TableName() string { return "group_policies" }

type CreateDatabaseRequest struct {
	DBMS         string `json:"dbms"`
	Name         string `json:"name" binding:"required"`
	Host         string `json:"host" binding:"required"`
	Port         int    `json:"port" binding:"required"`
	DatabaseName string `json:"database_name" binding:"required"`
	Username     string `json:"username" binding:"required"`
	Password     string `json:"password" binding:"required"`
	SSLMode      string `json:"ssl_mode"`
}
type UpdateDatabaseRequest struct {
	DBMS         string `json:"dbms"`
	Name         string `json:"name"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	DatabaseName string `json:"database_name"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	SSLMode      string `json:"ssl_mode"`
}
type PolicyRequest struct {
	GroupID uuid.UUID `json:"group_id" binding:"required"`
	Effect  string    `json:"effect" binding:"required"`
}

func (r CreateDatabaseRequest) Validate() error {
	if !validDBMS(r.DBMS) {
		return fmt.Errorf("dbms must be postgres or mariadb")
	}
	if validateDBMSSSLMode(normalizeDBMS(r.DBMS), normalizeSSLModeForDBMS(r.DBMS, r.SSLMode)) == "" {
		return fmt.Errorf("unsupported ssl_mode for dbms")
	}
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > 100 {
		return fmt.Errorf("name is required and must be at most 100 characters")
	}
	if strings.TrimSpace(r.Host) == "" || len(r.Host) > 253 {
		return fmt.Errorf("invalid host")
	}
	if r.Port < 1 || r.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if strings.TrimSpace(r.DatabaseName) == "" || len(r.DatabaseName) > 128 {
		return fmt.Errorf("invalid database_name")
	}
	if strings.TrimSpace(r.Username) == "" || len(r.Username) > 128 {
		return fmt.Errorf("invalid username")
	}
	return validateHost(r.Host)
}
func validateHost(host string) error {
	host = strings.TrimSpace(host)
	if strings.ContainsAny(host, " /\\@?#") || strings.Contains(host, "://") {
		return fmt.Errorf("host must be a hostname or IP address, not a URL")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !allowPrivateTargets() && (!ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
			return fmt.Errorf("private, loopback, and link-local target addresses are disabled")
		}
		if !allowedHost(host) {
			return fmt.Errorf("target address is not in TARGET_DB_ALLOWED_HOSTS")
		}
	} else {
		if strings.EqualFold(host, "localhost") && !allowPrivateTargets() {
			return fmt.Errorf("localhost targets are disabled")
		}
		if !allowedHost(host) && !(allowPrivateTargets() && (strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".internal"))) {
			return fmt.Errorf("target hostname is not in TARGET_DB_ALLOWED_HOSTS")
		}
	}
	return nil
}
func allowPrivateTargets() bool {
	return strings.EqualFold(strings.TrimSpace(getenv("ALLOW_PRIVATE_TARGETS")), "true")
}
func allowedHost(host string) bool {
	for _, h := range strings.Split(getenv("TARGET_DB_ALLOWED_HOSTS"), ",") {
		if strings.EqualFold(strings.TrimSpace(h), host) && strings.TrimSpace(h) != "" {
			return true
		}
	}
	return false
}

// getenv is isolated for tests.
var getenv = os.Getenv

func validateSSLMode(mode string) string {
	if mode == "" {
		return "verify-full"
	}
	if mode != "verify-full" && mode != "verify-ca" && mode != "require" && mode != "disable" {
		return ""
	}
	return mode
}
func validateIdentifier(s string) bool {
	return s != "" && len(s) <= 63 && !strings.ContainsRune(s, 0)
}

const (
	DBMSPostgres = "postgres"
	DBMSMariaDB  = "mariadb"
)

func normalizeDBMS(v string) string {
	if strings.TrimSpace(v) == "" {
		return DBMSPostgres
	}
	return strings.ToLower(strings.TrimSpace(v))
}
func validDBMS(v string) bool { v = normalizeDBMS(v); return v == DBMSPostgres || v == DBMSMariaDB }
func normalizeSSLModeForDBMS(engine, mode string) string {
	if strings.TrimSpace(mode) != "" {
		return strings.TrimSpace(mode)
	}
	if normalizeDBMS(engine) == DBMSMariaDB {
		return "disable"
	}
	return "verify-full"
}
func validateDBMSSSLMode(engine, mode string) string {
	if normalizeDBMS(engine) == DBMSMariaDB {
		if mode == "disable" || mode == "require" {
			return mode
		}
		return ""
	}
	return validateSSLMode(mode)
}
