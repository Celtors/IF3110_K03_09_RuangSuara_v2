package sessionmanager

import (
	"backend/access_control/models"
	"backend/access_control/utility"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var ErrUnauthenticated = errors.New("a valid active session is required")
var ErrNoSession = errors.New("session cookie is missing")
var rotation_interval = 1 * time.Minute
const grace_period_duration = 2 * time.Minute

func RotateSession(session models.SSOSession, c *gin.Context, db *gorm.DB) {
	rotation := session.LatestRotation(db)
	var last_rotated_at time.Time
	if rotation != nil {
		last_rotated_at = rotation.RotatedAt
	} else {
		last_rotated_at = session.CreatedAt
	}
	if last_rotated_at.Add(rotation_interval).Before(time.Now()) {
		session_token, _ := utility.CryptoRandString(16)
		new_token_hash := utility.HashToken(session_token)
		old_token_hash := session.SessionTokenHash
		db.Model(&models.SSOSession{}).Where("id = ?", session.ID).Updates(models.SSOSession{SessionTokenHash: new_token_hash})
		c.SetCookie("ssid", session_token, int(session_absolute_exp_duration.Seconds() - time.Since(session.CreatedAt).Seconds()), "/", "localhost", true, true)
		new_rotation := models.SessionRotation{
			SessionId: session.ID,
			OldSessionTokenHash: old_token_hash,
			NewSessionTokenHash: new_token_hash,
			Status: "Success",
			RotatedAt: time.Now(),
		}

		err := db.Create(&new_rotation).Error
		if err != nil {
			InternalServerResponse(c)
			return
		}
	}
}

// CurrentUser is the shared cookie/session validation used by application modules.
func CurrentUser(c *gin.Context, db *gorm.DB) (models.User, error) {
	var user models.User
	cookie, err := c.Cookie("ssid")
	if err != nil || cookie == "" {
		return user, ErrNoSession
	}
	var session models.SSOSession
	err = db.WithContext(c.Request.Context()).Where("session_token_hash = ? AND status = ?", utility.HashToken(cookie), "Active").First(&session).Error
	valid := false
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && !session.IsValid()) {
		rotation := session.LatestRotation(db)
		var last_rotated_at time.Time
		if rotation != nil {
			last_rotated_at = rotation.RotatedAt
			if last_rotated_at.Add(grace_period_duration).Before(time.Now()) {
				return user, ErrUnauthenticated
			}	
			err = nil
		} else {
			return user, ErrUnauthenticated
		}
	}
	session.MarkActivity(db)
	RotateSession(session, c, db)

	if err != nil && !valid {
		return user, err
	}


	err = db.WithContext(c.Request.Context()).Where("id = ? AND status = ?", session.UserId, "Active").First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return user, ErrUnauthenticated
	}
	return user, err
}

// CSRFToken is bound to the HttpOnly session secret and changes with the session.
// It is exposed only through an authenticated same-origin session response.
func CSRFToken(request *http.Request) string {
	cookie, err := request.Cookie("ssid")
	if err != nil || cookie.Value == "" {
		return ""
	}
	return utility.HashToken("ruangsuara:csrf:" + cookie.Value)
}

func ValidateCSRF(request *http.Request) error {
	expected := CSRFToken(request)
	origin, err := url.Parse(request.Header.Get("Origin"))
	if err != nil || origin == nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host != request.Host || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return errors.New("invalid request origin")
	}
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(request.Header.Get("X-CSRF-Token"))) != 1 {
		return errors.New("invalid CSRF token")
	}
	return nil
}
