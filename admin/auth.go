package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookieName = "vividp_session"
	sessionMaxAge     = 24 * time.Hour
)

// Auth validates credentials from env vars and issues HMAC-signed session cookies.
// Stateless — no server-side session store. The cookie carries a signed nonce+timestamp.
type Auth struct {
	username string
	password string
	secret   []byte
}

// NewAuth creates an Auth from config.
// Returns nil (auth disabled) if ADMIN_PASSWORD is not set.
// Returns an error if ADMIN_PASSWORD is set but ADMIN_SESSION_SECRET is missing.
func NewAuth(cfg Config) (*Auth, error) {
	if cfg.AdminPassword == "" {
		return nil, nil // auth disabled — dev mode
	}
	if cfg.SessionSecret == "" {
		return nil, fmt.Errorf("ADMIN_SESSION_SECRET must be set when ADMIN_PASSWORD is configured")
	}
	return &Auth{
		username: cfg.AdminUsername,
		password: cfg.AdminPassword,
		secret:   []byte(cfg.SessionSecret),
	}, nil
}

// Require wraps a handler to enforce a valid session cookie.
// If auth is nil (disabled), the handler is returned as-is.
func (a *Auth) Require(next http.HandlerFunc) http.HandlerFunc {
	if a == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.validateSession(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

// HandleLogin validates credentials and sets a session cookie.
func (a *Auth) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if a == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}

	if req.Username != a.username || req.Password != a.password {
		time.Sleep(300 * time.Millisecond) // slow down brute-force
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, err := a.newToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server error")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		HttpOnly: true,
		Path:     "/",
		MaxAge:   int(sessionMaxAge.Seconds()),
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// HandleLogout clears the session cookie.
func (a *Auth) HandleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		HttpOnly: true,
		Path:     "/",
		MaxAge:   -1,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// HandleMe returns 200 if the session is valid, 401 otherwise.
// The frontend calls this on load to decide whether to show the login screen.
func (a *Auth) HandleMe(w http.ResponseWriter, r *http.Request) {
	if a == nil || a.validateSession(r) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	writeError(w, http.StatusUnauthorized, "unauthorized")
}

// newToken returns "nonce|timestamp|HMAC" — signed so we can verify without storage.
func (a *Auth) newToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	payload := hex.EncodeToString(buf) + "|" + strconv.FormatInt(time.Now().Unix(), 10)
	return payload + "|" + a.sign(payload), nil
}

func (a *Auth) sign(data string) string {
	h := hmac.New(sha256.New, a.secret)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func (a *Auth) validateSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	// Format: nonce|timestamp|mac — split on last pipe to get payload vs mac.
	idx := strings.LastIndex(cookie.Value, "|")
	if idx < 0 {
		return false
	}
	payload, mac := cookie.Value[:idx], cookie.Value[idx+1:]
	if !hmac.Equal([]byte(mac), []byte(a.sign(payload))) {
		return false
	}
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return false
	}
	ts, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	return time.Since(time.Unix(ts, 0)) < sessionMaxAge
}
