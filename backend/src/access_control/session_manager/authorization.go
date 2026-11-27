package sessionmanager

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"backend/access_control/models"
	"backend/access_control/utility"
	"backend/internal/telemetry"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	absoluteLifetime = 8 * time.Hour
	idleTimeout      = 30 * time.Minute
	rotationInterval = 15 * time.Minute
	previousGrace    = 30 * time.Second
	sessionCookie    = "ssid"
	CSRFHeader       = "X-CSRF-Token"
)

var ErrUnauthenticated = errors.New("a valid active session is required")
var ErrCSRF = errors.New("CSRF token or request origin is invalid")

// Initialize adds the session fields on both fresh and existing platform databases.
func Initialize(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Exec(`
		ALTER TABLE sso_sessions ADD COLUMN IF NOT EXISTS authorization_stamp text NOT NULL DEFAULT '';
		ALTER TABLE session_rotations ADD COLUMN IF NOT EXISTS continuation_nonce text NOT NULL DEFAULT ''
	`).Error
}

// resolve holds the account and then the session lock. Login and deactivation
// take the same locks in that order. Recheck the hash after a concurrent rotation.
func resolve(tx *gorm.DB, token string) (models.User, models.SSOSession, models.SessionRotation, error) {
	var owner struct {
		ID     uuid.UUID
		UserID uuid.UUID
	}
	hash := utility.HashToken(token)
	err := tx.Raw(`
		SELECT id, user_id FROM sso_sessions WHERE session_token_hash = ?
		UNION ALL
		SELECT s.id, s.user_id FROM session_rotations r
		JOIN sso_sessions s ON s.id = r.session_id
		WHERE r.old_session_token_hash = ? AND r.new_session_token_hash = s.session_token_hash
		LIMIT 1
	`, hash, hash).Take(&owner).Error
	var user models.User
	var session models.SSOSession
	var previous models.SessionRotation
	if err != nil {
		return user, session, previous, sessionLookupError(err)
	}
	if err = tx.Clauses(clause.Locking{Strength: "SHARE"}).Take(&user, "id = ?", owner.UserID).Error; err != nil {
		return user, session, previous, sessionLookupError(err)
	}
	if !user.IsActive() {
		return user, session, previous, ErrUnauthenticated
	}
	if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&session, "id = ?", owner.ID).Error; err != nil {
		return user, session, previous, sessionLookupError(err)
	}
	now := time.Now()
	lastActivity := session.CreatedAt
	if session.LastActivityAt != nil {
		lastActivity = *session.LastActivityAt
	}
	session.ExpiresAt = minTime(session.ExpiresAt, session.CreatedAt.Add(absoluteLifetime))
	if session.Status != "Active" || session.RevokedAt != nil {
		return user, session, previous, ErrUnauthenticated
	}
	if !now.Before(session.ExpiresAt) || !now.Before(lastActivity.Add(idleTimeout)) {
		slog.InfoContext(tx.Statement.Context, "authentication.session_expired", telemetry.ID("actor_id", user.ID.String()))
		return user, session, previous, ErrUnauthenticated
	}
	if hash != session.SessionTokenHash {
		err = tx.Where("session_id = ? AND old_session_token_hash = ? AND new_session_token_hash = ? AND status = ? AND rotated_at > ?",
			session.ID, hash, session.SessionTokenHash, "Success", now.Add(-previousGrace)).Take(&previous).Error
	}
	return user, session, previous, sessionLookupError(err)
}

func sessionLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrUnauthenticated
	}
	return err
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// CurrentUser authenticates and applies CSRF before updating activity or rotating.
func CurrentUser(c *gin.Context, db *gorm.DB) (models.User, error) {
	ctx := c.Request.Context()
	token, err := c.Cookie(sessionCookie)
	if err != nil || token == "" {
		return models.User{}, ErrUnauthenticated
	}

	var user models.User
	var expires time.Time
	var replacement, reason string
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		principal, session, previous, err := resolve(tx, token)
		if err != nil {
			return err
		}
		user, expires = principal, session.ExpiresAt
		if err = ValidateCSRF(c.Request); err != nil {
			return err
		}
		stamp, err := models.AuthorizationStamp(tx, user.ID)
		if err != nil {
			return err
		}
		now := time.Now()
		changed := session.AuthorizationStamp != stamp
		updates := map[string]any{"last_activity_at": now}
		switch {
		case previous.ID != uuid.Nil:
			if changed {
				return ErrUnauthenticated
			}
			if previous.ContinuationNonce != "" {
				replacement = continuation(token, previous.ContinuationNonce)
			}
		default:
			lastRotation := session.CreatedAt
			var rotation models.SessionRotation
			err = tx.Where("session_id = ? AND new_session_token_hash = ?", session.ID, session.SessionTokenHash).Take(&rotation).Error
			if err == nil {
				lastRotation = rotation.RotatedAt
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if changed || !now.Before(lastRotation.Add(rotationInterval)) {
				reason = "periodic"
				rotation = models.SessionRotation{
					SessionId: session.ID, OldSessionTokenHash: session.SessionTokenHash,
					Status: "Success", RotatedAt: now,
				}
				random, err := utility.CryptoRandString(32)
				if err != nil {
					return err
				}
				if changed {
					reason = "privilege_change"
					rotation.Status = "Privilege"
					replacement = random
				} else {
					rotation.ContinuationNonce = random
					replacement = continuation(token, random)
				}
				rotation.NewSessionTokenHash = utility.HashToken(replacement)
				if err = tx.Create(&rotation).Error; err != nil {
					return err
				}
				updates["session_token_hash"] = rotation.NewSessionTokenHash
				updates["authorization_stamp"] = stamp
			}
		}
		return tx.Model(&session).Updates(updates).Error
	})
	if user.ID != uuid.Nil {
		ctx = telemetry.With(ctx, telemetry.ID("actor_id", user.ID.String()))
		c.Request = c.Request.WithContext(ctx)
	}
	if err != nil {
		if errors.Is(err, ErrCSRF) {
			slog.WarnContext(ctx, "authorization.csrf", "status", "denied")
		} else {
			slog.WarnContext(ctx, "authentication.session", "status", "failed")
		}
		return models.User{}, err
	}

	if replacement != "" {
		setSessionCookie(c, replacement, expires)
		token = replacement
	}
	if reason != "" {
		slog.InfoContext(ctx, "authentication.session_rotated", "status", "succeeded", "reason", reason)
	}
	c.Set(CSRFHeader, csrfToken(token))
	slog.InfoContext(ctx, "authentication.session", "status", "succeeded")
	return user, nil
}

// The nonce and token hashes cannot reconstruct the replacement without
// possession of the previous opaque token. This is used only for periodic grace.
func continuation(previous, nonce string) string {
	mac := hmac.New(sha256.New, []byte(nonce))
	mac.Write([]byte(previous))
	return hex.EncodeToString(mac.Sum(nil))
}

func setSessionCookie(c *gin.Context, token string, expires time.Time) {
	cookie := &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expires,
	}
	if token == "" {
		cookie.MaxAge = -1
	}
	http.SetCookie(c.Writer, cookie)
}

func csrfToken(token string) string {
	return utility.HashToken("ruangsuara:csrf:" + token)
}

func validOrigin(request *http.Request) bool {
	values := request.Header.Values("Origin")
	if len(values) != 1 {
		return false
	}
	origin, err := url.Parse(values[0])
	return err == nil && (origin.Scheme == "https" || (origin.Scheme == "http" && request.TLS == nil)) &&
		strings.EqualFold(origin.Host, request.Host) && origin.User == nil &&
		origin.Path == "" && origin.RawQuery == "" && !origin.ForceQuery && origin.Fragment == ""
}

func ValidateCSRF(request *http.Request) error {
	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	cookie, err := request.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" || !validOrigin(request) ||
		subtle.ConstantTimeCompare([]byte(csrfToken(cookie.Value)), []byte(request.Header.Get(CSRFHeader))) != 1 {
		return ErrCSRF
	}
	return nil
}
