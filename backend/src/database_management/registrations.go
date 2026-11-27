package databasemanagement

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"backend/internal/targetdb"
	"backend/internal/telemetry"

	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errDuplicateTarget     = errors.New("this physical database is already registered")
	errRegistrationInUse   = errors.New("migration definitions or history depend on this registration")
	errRegistrationChanged = errors.New("the registration changed during verification; try again")
	errLegacyTargetUnbound = errors.New("verify existing targets with migration history using Test Connection before registering a new target")
)

// Initialize binds new registrations through a unique physical fingerprint.
// Existing rows are bound lazily when their target can be verified.
func Initialize(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Exec(`
		ALTER TABLE databases ADD COLUMN IF NOT EXISTS physical_fingerprint text;
		CREATE UNIQUE INDEX IF NOT EXISTS databases_physical_fingerprint_key
		ON databases (physical_fingerprint) WHERE physical_fingerprint IS NOT NULL
	`).Error
}

func registrationError(c *gin.Context, err error) {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, ErrDatabaseNotFound):
		fail(c, http.StatusNotFound, "DATABASE_NOT_FOUND", "Database not found")
	case errors.Is(err, targetdb.ErrPlatform):
		fail(c, http.StatusForbidden, "PLATFORM_TARGET", targetdb.ErrPlatform.Error())
	case errors.Is(err, targetdb.ErrChanged), errors.Is(err, errRegistrationInUse),
		errors.Is(err, errRegistrationChanged), errors.Is(err, errLegacyTargetUnbound):
		fail(c, http.StatusConflict, "TARGET_CONFLICT", err.Error())
	case errors.Is(err, errDuplicateTarget),
		errors.As(err, &pgErr) && pgErr.ConstraintName == "databases_physical_fingerprint_key":
		fail(c, http.StatusConflict, "DUPLICATE_TARGET", errDuplicateTarget.Error())
	case errors.Is(err, errTargetUnavailable):
		fail(c, http.StatusBadGateway, "TARGET_UNAVAILABLE", errTargetUnavailable.Error())
	default:
		fail(c, http.StatusInternalServerError, "DATABASE_UPDATE_FAILED", "Could not save the database registration")
	}
}

func (h *Handler) createDatabase(c *gin.Context) {
	var req CreateDatabaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "INVALID_REQUEST", "Invalid database configuration")
		return
	}
	if err := req.Validate(); err != nil {
		fail(c, 400, "INVALID_REQUEST", err.Error())
		return
	}
	ciphertext, err := encryptPassword(req.Password)
	if err != nil {
		fail(c, 500, "CREDENTIALS_NOT_CONFIGURED", "Secure credential encryption is not configured")
		return
	}
	db := Database{
		Name: strings.TrimSpace(req.Name), Host: strings.TrimSpace(req.Host), Port: req.Port,
		DatabaseName: req.DatabaseName, Username: req.Username, PasswordCiphertext: ciphertext,
		SSLMode: validateSSLMode(strings.TrimSpace(req.SSLMode)),
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	platform := h.platform.WithContext(ctx)
	fingerprint, _, err := h.provider.probe(ctx, platform, db, req.Password)
	if err != nil {
		registrationError(c, err)
		return
	}
	if err := protectUnboundHistory(platform); err != nil {
		registrationError(c, err)
		return
	}
	db.PhysicalFingerprint = &fingerprint
	if err := platform.Create(&db).Error; err != nil {
		registrationError(c, err)
		return
	}
	ctx = telemetry.With(c.Request.Context(), telemetry.ID("database_id", db.ID.String()))
	c.Request = c.Request.WithContext(ctx)
	slog.InfoContext(ctx, "database.registered", "status", "succeeded")
	c.JSON(http.StatusCreated, db)
}

// Use the migration lock key so the dependency check and registration write
// cannot interleave with a definition being saved or target work being started.
func lockRegistration(tx *gorm.DB, id uuid.UUID) error {
	var locked bool
	err := tx.Raw("SELECT pg_catalog.pg_try_advisory_xact_lock(pg_catalog.hashtextextended(?,781349))", id.String()).Scan(&locked).Error
	if err == nil && !locked {
		return errRegistrationInUse
	}
	return err
}

// Change requests and execution attempts retain their definition by foreign key.
func hasMigrationDependencies(tx *gorm.DB, id uuid.UUID) (bool, error) {
	var used bool
	err := tx.Raw("SELECT EXISTS(SELECT 1 FROM dmp_migration.definitions WHERE database_id = ?)", id.String()).Scan(&used).Error
	return used, err
}

// Pre-upgrade work can hold a reservation before its registration has a
// fingerprint. Leave its physical target available for the first verified bind.
// New requests and attempts already open a verified target before persisting work.
func protectUnboundHistory(db *gorm.DB) error {
	var unbound bool
	err := db.Raw(`SELECT EXISTS (
		SELECT 1 FROM databases d
		WHERE d.physical_fingerprint IS NULL AND (
			EXISTS (SELECT 1 FROM dmp_migration.attempts a WHERE a.database_id = d.id::text)
			OR EXISTS (SELECT 1 FROM dmp_migration.change_requests r WHERE r.database_id = d.id::text)
		)
	)`).Scan(&unbound).Error
	if err == nil && unbound {
		return errLegacyTargetUnbound
	}
	return err
}

func (h *Handler) updateDatabase(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	var req UpdateDatabaseRequest
	if err != nil || c.ShouldBindJSON(&req) != nil {
		fail(c, 400, "INVALID_REQUEST", "Invalid database update")
		return
	}
	if req.Host != "" {
		if err := validateHost(req.Host); err != nil {
			fail(c, 400, "INVALID_REQUEST", err.Error())
			return
		}
	}
	if req.Port < 0 || req.Port > 65535 {
		fail(c, 400, "INVALID_REQUEST", "Port must be between 1 and 65535")
		return
	}
	if req.SSLMode != "" && validateSSLMode(strings.TrimSpace(req.SSLMode)) == "" {
		fail(c, 400, "INVALID_REQUEST", "Invalid PostgreSQL SSL mode")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	var db Database
	err = h.platform.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockRegistration(tx, id); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&db, "id = ?", id).Error; err != nil {
			return err
		}
		old := db
		if req.Name != "" {
			db.Name = strings.TrimSpace(req.Name)
		}
		if req.Host != "" {
			db.Host = strings.TrimSpace(req.Host)
		}
		if req.Port != 0 {
			db.Port = req.Port
		}
		if req.DatabaseName != "" {
			db.DatabaseName = req.DatabaseName
		}
		if req.Username != "" {
			db.Username = req.Username
		}
		if req.SSLMode != "" {
			db.SSLMode = validateSSLMode(strings.TrimSpace(req.SSLMode))
		}
		retargeted := db.Host != old.Host || db.Port != old.Port || db.DatabaseName != old.DatabaseName
		if retargeted || db.Username != old.Username || db.SSLMode != old.SSLMode || req.Password != "" {
			if retargeted {
				used, err := hasMigrationDependencies(tx, id)
				if err != nil {
					return err
				}
				if used {
					return errRegistrationInUse
				}
				if err := protectUnboundHistory(tx); err != nil {
					return err
				}
				db.PhysicalFingerprint = nil
			}
			password := req.Password
			if password != "" {
				ciphertext, err := encryptPassword(password)
				if err != nil {
					return err
				}
				db.PasswordCiphertext = ciphertext
			} else {
				var err error
				password, err = decryptPassword(db.PasswordCiphertext)
				if err != nil {
					return errTargetUnavailable
				}
			}
			fingerprint, recoveryID, err := h.provider.probe(ctx, tx, db, password)
			if err != nil {
				return err
			}
			if db.PhysicalFingerprint == nil {
				if err := checkLegacyIdentity(tx, id, recoveryID); err != nil {
					return err
				}
			}
			db.PhysicalFingerprint = &fingerprint
		}
		return tx.Save(&db).Error
	})
	if err != nil {
		registrationError(c, err)
		return
	}
	slog.InfoContext(c.Request.Context(), "database.updated", "status", "succeeded")
	c.JSON(200, db)
}

func (h *Handler) deleteDatabase(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		fail(c, 400, "INVALID_ID", "Database ID is invalid")
		return
	}
	err = h.platform.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockRegistration(tx, id); err != nil {
			return err
		}
		used, err := hasMigrationDependencies(tx, id)
		if err != nil {
			return err
		}
		if used {
			return errRegistrationInUse
		}
		result := tx.Delete(&Database{}, "id = ?", id)
		if result.Error == nil && result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return result.Error
	})
	if err != nil {
		registrationError(c, err)
		return
	}
	slog.InfoContext(c.Request.Context(), "database.deleted", "status", "succeeded")
	c.Status(204)
}

func (h *Handler) testConnection(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	pool, err := h.provider.PostgreSQL(ctx, c.Param("id"))
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		message := errTargetUnavailable.Error()
		if errors.Is(err, targetdb.ErrPlatform) || errors.Is(err, targetdb.ErrChanged) || errors.Is(err, errDuplicateTarget) {
			message = err.Error()
		}
		slog.WarnContext(ctx, "database.connection_test", "status", "failed")
		c.JSON(200, gin.H{"success": false, "message": message})
		return
	}
	slog.InfoContext(ctx, "database.connection_test", "status", "succeeded")
	c.JSON(200, gin.H{"success": true, "message": "Connection successful"})
}
