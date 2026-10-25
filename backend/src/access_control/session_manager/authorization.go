package sessionmanager

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"

	"backend/access_control/models"
	"backend/access_control/utility"
	"gorm.io/gorm"
)

var ErrUnauthenticated = errors.New("a valid active session is required")
var ErrNoSession = errors.New("session cookie is missing")

// CurrentUser is the shared cookie/session validation used by application modules.
func CurrentUser(ctx context.Context, request *http.Request, db *gorm.DB) (models.User, error) {
	var user models.User
	cookie, err := request.Cookie("ssid")
	if err != nil || cookie.Value == "" {
		return user, ErrNoSession
	}
	var session models.SSOSession
	err = db.WithContext(ctx).Where("session_token_hash = ? AND status = ?", utility.HashToken(cookie.Value), "Active").First(&session).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && !session.IsValid()) {
		return user, ErrUnauthenticated
	}
	session.MarkActivity(db)
	if err != nil {
		return user, err
	}
	err = db.WithContext(ctx).Where("id = ? AND status = ?", session.UserId, "Active").First(&user).Error
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
