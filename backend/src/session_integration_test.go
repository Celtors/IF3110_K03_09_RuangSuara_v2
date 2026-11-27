package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"backend/access_control/utility"
	"backend/migration"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSessionAndAdminIntegration(t *testing.T) {
	dsn := os.Getenv("SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("set SESSION_TEST_DSN to a fresh disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("could not open disposable platform database")
	}
	defer pool.Close()
	var initialized bool
	if err = pool.QueryRow(ctx, "SELECT to_regclass('public.users') IS NOT NULL").Scan(&initialized); err != nil || initialized {
		t.Fatal("session integration test requires a fresh disposable platform database")
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal("could not apply session fixture:", err)
		}
	}
	base, err := os.ReadFile("../../database_schema/access-control/access-control-db.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(base))
	t.Setenv("PLATFORM_DATABASE_DSN", dsn)
	gin.SetMode(gin.TestMode)
	router, closeRouter, err := newRouter(&migration.Service{}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRouter()

	const origin = "https://ruangsuara.test"
	const me = "/session-manager/me"
	const logout = "/session-manager/logout"
	send := func(method, path, body, requestOrigin, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(ctx, method, origin+path, strings.NewReader(body))
		if requestOrigin != "" {
			req.Header.Set("Origin", requestOrigin)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	wantStatus := func(response *httptest.ResponseRecorder, want int) {
		t.Helper()
		if response.Code != want {
			t.Fatalf("status = %d, want %d", response.Code, want)
		}
	}
	cookieFrom := func(response *httptest.ResponseRecorder) *http.Cookie {
		for _, cookie := range response.Result().Cookies() {
			if cookie.Name == "ssid" {
				return cookie
			}
		}
		return nil
	}
	login := func(username string) *http.Cookie {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"username": username, "password": "password123"})
		response := send(http.MethodPost, "/session-manager/login", string(body), origin, "", nil)
		wantStatus(response, http.StatusOK)
		cookie := cookieFrom(response)
		if cookie == nil || cookie.Value == "" || !cookie.HttpOnly || !cookie.Secure ||
			cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode {
			t.Fatal("login must set a Secure, Lax, host-only HttpOnly cookie")
		}
		if remaining := time.Until(cookie.Expires); remaining < 8*time.Hour-3*time.Second || remaining > 8*time.Hour {
			t.Fatal("login did not set the agreed absolute expiration")
		}
		return cookie
	}
	proof := func(cookie *http.Cookie) string {
		t.Helper()
		res := send(http.MethodGet, me, "", "", "", cookie)
		wantStatus(res, http.StatusOK)
		header := res.Header().Get("X-CSRF-Token")
		if header == "" || strings.Contains(res.Body.String(), header) {
			t.Fatal("expected the CSRF proof in the response header only")
		}
		return header
	}
	makeRotationDue := func(cookie *http.Cookie) time.Time {
		t.Helper()
		created := time.Now().UTC().Truncate(time.Second).Add(-15 * time.Minute)
		expires := created.Add(8 * time.Hour)
		exec("UPDATE sso_sessions SET created_at=$1, expires_at=$2 WHERE session_token_hash=$3",
			created, expires, utility.HashToken(cookie.Value))
		return expires
	}

	t.Run("first request at idle and absolute expiration", func(t *testing.T) {
		for _, column := range []string{"last_activity_at", "expires_at"} {
			cookie := login("alice")
			deadline := time.Now()
			if column == "last_activity_at" {
				deadline = deadline.Add(-30 * time.Minute)
			}
			exec("UPDATE sso_sessions SET "+column+"=$1 WHERE session_token_hash=$2", deadline, utility.HashToken(cookie.Value))
			res := send(http.MethodGet, me, "", "", "", cookie)
			wantStatus(res, http.StatusUnauthorized)
			if cookieFrom(res) != nil {
				t.Fatal("expired request received a replacement session")
			}
		}
	})

	t.Run("concurrent rotation, grace and logout", func(t *testing.T) {
		previous := login("alice")
		expires := makeRotationDue(previous)
		csrf := utility.HashToken("ruangsuara:csrf:" + previous.Value)
		start := make(chan struct{})
		responses := make(chan *httptest.ResponseRecorder, 6)
		for range cap(responses) {
			go func() {
				<-start
				responses <- send(http.MethodGet, me, "", "", "", previous)
			}()
		}
		close(start)
		var current *http.Cookie
		for range cap(responses) {
			res := <-responses
			wantStatus(res, http.StatusOK)
			got := cookieFrom(res)
			if got == nil || got.Value == previous.Value || !got.Expires.Equal(expires) || (current != nil && got.Value != current.Value) {
				t.Fatal("concurrent requests did not converge on one replacement with the original expiration")
			}
			current = got
		}
		var rotated time.Time
		err := pool.QueryRow(ctx, "SELECT rotated_at FROM session_rotations WHERE old_session_token_hash=$1",
			utility.HashToken(previous.Value)).Scan(&rotated)
		if err != nil {
			t.Fatal("missing durable rotation")
		}
		exec("UPDATE session_rotations SET rotated_at=$1 WHERE old_session_token_hash=$2",
			time.Now().Add(-30*time.Second), utility.HashToken(previous.Value))
		wantStatus(send(http.MethodGet, me, "", "", "", previous), http.StatusUnauthorized)
		exec("UPDATE session_rotations SET rotated_at=$1 WHERE old_session_token_hash=$2", rotated, utility.HashToken(previous.Value))

		exec("ALTER TABLE sso_sessions ADD CONSTRAINT fixture_failed_logout CHECK (status <> 'Revoked') NOT VALID")
		failed := send(http.MethodPost, logout, "", origin, csrf, previous)
		wantStatus(failed, http.StatusServiceUnavailable)
		if cookieFrom(failed) != nil {
			t.Fatal("failed revocation cleared the browser cookie")
		}
		exec("ALTER TABLE sso_sessions DROP CONSTRAINT fixture_failed_logout")
		res := send(http.MethodPost, logout, "", origin, csrf, previous)
		wantStatus(res, http.StatusOK)
		clear := cookieFrom(res)
		if clear == nil || clear.Value != "" || clear.MaxAge != -1 || !clear.Secure || !clear.HttpOnly ||
			clear.Path != current.Path || clear.Domain != current.Domain || clear.SameSite != current.SameSite {
			t.Fatal("logout did not clear the session cookie with the original attributes")
		}
		wantStatus(send(http.MethodGet, me, "", "", "", previous), http.StatusUnauthorized)
		wantStatus(send(http.MethodGet, me, "", "", "", current), http.StatusUnauthorized)
		wantStatus(send(http.MethodPost, logout, "", origin, csrf, previous), http.StatusOK)
	})

	t.Run("failed rotation does not persist or issue the replacement", func(t *testing.T) {
		cookie := login("alice")
		makeRotationDue(cookie)
		exec("ALTER TABLE session_rotations ADD CONSTRAINT fixture_failed_rotation CHECK (status <> 'Success') NOT VALID")
		res := send(http.MethodGet, me, "", "", "", cookie)
		wantStatus(res, http.StatusServiceUnavailable)
		if cookieFrom(res) != nil {
			t.Fatal("failed rotation issued a replacement cookie")
		}
		exec("ALTER TABLE session_rotations DROP CONSTRAINT fixture_failed_rotation")
		wantStatus(send(http.MethodGet, me, "", "", "", cookie), http.StatusOK)
	})

	t.Run("admin CSRF, privilege renewal and deactivation", func(t *testing.T) {
		wantStatus(send(http.MethodPost, "/session-manager/login", "{}", "https://other.test", "", nil), http.StatusForbidden)
		admin := login("admin")
		alice := login("alice")
		makeRotationDue(alice)
		periodic := cookieFrom(send(http.MethodGet, me, "", "", "", alice))
		if periodic == nil {
			t.Fatal("periodic fixture did not rotate")
		}
		var aliceID, adminGroup string
		if err := pool.QueryRow(ctx, "SELECT id::text FROM users WHERE username='alice'").Scan(&aliceID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "SELECT id::text FROM groups_ WHERE name='platform-admins'").Scan(&adminGroup); err != nil {
			t.Fatal(err)
		}
		member, _ := json.Marshal(map[string]string{"user_id": aliceID, "group_id": adminGroup})
		body := string(member)
		csrf := proof(admin)
		membership := "/admin-control-panel/groups/users"
		leader := "/admin-control-panel/groups/leader"
		wantStatus(send(http.MethodPost, membership, body, origin, "", admin), http.StatusForbidden)
		wantStatus(send(http.MethodPost, membership, body, "https://other.test", csrf, admin), http.StatusForbidden)
		wantStatus(send(http.MethodPost, leader, body, origin, csrf, admin), http.StatusBadRequest)
		wantStatus(send(http.MethodPost, membership, body, origin, csrf, admin), http.StatusCreated)
		wantStatus(send(http.MethodGet, me, "", "", "", alice), http.StatusUnauthorized)
		elevated := send(http.MethodGet, me, "", "", "", periodic)
		wantStatus(elevated, http.StatusOK)
		newCookie := cookieFrom(elevated)
		if newCookie == nil || newCookie.Value == periodic.Value || !strings.Contains(elevated.Body.String(), "\"admin\":true") {
			t.Fatal("current session did not renew upon elevation")
		}
		wantStatus(send(http.MethodGet, me, "", "", "", periodic), http.StatusUnauthorized)

		wantStatus(send(http.MethodPost, leader, body, origin, csrf, admin), http.StatusCreated)
		wantStatus(send(http.MethodGet, leader+"?id="+adminGroup, "", "", "", admin), http.StatusOK)
		wantStatus(send(http.MethodDelete, membership, body, origin, csrf, admin), http.StatusOK)
		wantStatus(send(http.MethodGet, leader+"?id="+adminGroup, "", "", "", admin), http.StatusNotFound)
		wantStatus(send(http.MethodDelete, membership, body, origin, csrf, admin), http.StatusNotFound)
		wantStatus(send(http.MethodPost, membership, body, origin, csrf, admin), http.StatusCreated)
		wantStatus(send(http.MethodPost, leader, body, origin, csrf, admin), http.StatusCreated)
		update, _ := json.Marshal(map[string]string{"id": aliceID, "status": "Inactive"})
		wantStatus(send(http.MethodPatch, "/admin-control-panel/users", string(update), origin, csrf, admin), http.StatusOK)
		wantStatus(send(http.MethodGet, leader+"?id="+adminGroup, "", "", "", admin), http.StatusNotFound)
		wantStatus(send(http.MethodPost, leader, body, origin, csrf, admin), http.StatusBadRequest)
		update, _ = json.Marshal(map[string]string{"id": aliceID, "status": "Active"})
		wantStatus(send(http.MethodPatch, "/admin-control-panel/users", string(update), origin, csrf, admin), http.StatusOK)
		wantStatus(send(http.MethodGet, me, "", "", "", newCookie), http.StatusUnauthorized)
		fresh := login("alice")
		current := login("alice")
		wantStatus(send(http.MethodGet, me, "", "", "", fresh), http.StatusUnauthorized)

		const replacement = "fixture-new-password"
		update, _ = json.Marshal(map[string]string{"id": aliceID, "password": replacement})
		wantStatus(send(http.MethodPatch, "/admin-control-panel/users", string(update), origin, csrf, admin), http.StatusOK)
		wantStatus(send(http.MethodGet, me, "", "", "", current), http.StatusUnauthorized)
		loginBody, err := json.Marshal(map[string]string{"username": "alice", "password": replacement})
		if err != nil {
			t.Fatal(err)
		}
		wantStatus(send(http.MethodPost, "/session-manager/login", string(loginBody), origin, "", nil), http.StatusOK)
	})
}
