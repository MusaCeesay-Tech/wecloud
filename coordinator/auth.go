package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	sessionCookieName = "wecloud_session"
	magicLinkTTL      = 15 * time.Minute
	sessionTTL        = 30 * 24 * time.Hour
)

// publicBaseURL is the externally reachable coordinator origin used to build
// magic links. Override with WECLOUD_PUBLIC_URL in any real deployment.
func publicBaseURL() string {
	if v := os.Getenv("WECLOUD_PUBLIC_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://localhost:8080"
}

func randomToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("entropy failure: %v", err)
	}
	return hex.EncodeToString(b)
}

// ensureUser returns the user id for an email, creating the account and granting
// the one-time signup bonus on first sight.
func ensureUser(email string) (id string, created bool, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	mu.Lock()
	defer mu.Unlock()

	err = db.QueryRow(`SELECT id FROM users WHERE email = ?`, email).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if err != sql.ErrNoRows {
		return "", false, err
	}

	id = fmt.Sprintf("usr-%d", time.Now().UnixNano())
	tx, err := db.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO users (id, email) VALUES (?, ?)`, id, email); err != nil {
		return "", false, err
	}
	if _, err = tx.Exec(`INSERT INTO ledger (user_id, delta, reason) VALUES (?, ?, ?)`,
		id, signupBonus, reasonSignupBonus); err != nil {
		return "", false, err
	}
	if err = tx.Commit(); err != nil {
		return "", false, err
	}
	log.Printf("new user created: %s (%s) +%d credits", id, email, signupBonus)
	return id, true, nil
}

func createSession(userID string) (string, error) {
	token := randomToken()
	_, err := db.Exec(`INSERT INTO sessions (token, user_id, expires_at)
		VALUES (?, ?, datetime('now', ?))`,
		token, userID, fmt.Sprintf("+%d seconds", int(sessionTTL.Seconds())))
	return token, err
}

// userFromRequest resolves the session token from the Authorization header or
// session cookie and returns the owning user id (empty if unauthenticated).
func userFromRequest(r *http.Request) string {
	token := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	} else if c, err := r.Cookie(sessionCookieName); err == nil {
		token = c.Value
	}
	if token == "" {
		return ""
	}
	var userID string
	err := db.QueryRow(`SELECT user_id FROM sessions
		WHERE token = ? AND expires_at > datetime('now')`, token).Scan(&userID)
	if err != nil {
		return ""
	}
	return userID
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

// handleAuthRequest issues a magic link for an email address. In this milestone
// there is no SMTP integration yet, so the link (and token) are returned in the
// response and logged. TODO(M5): send via email only and stop echoing the token.
func handleAuthRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if email == "" || !strings.Contains(email, "@") {
		http.Error(w, "a valid email is required", http.StatusBadRequest)
		return
	}

	token := randomToken()
	mu.Lock()
	_, err := db.Exec(`INSERT INTO magic_links (token, email, expires_at)
		VALUES (?, ?, datetime('now', ?))`,
		token, email, fmt.Sprintf("+%d seconds", int(magicLinkTTL.Seconds())))
	mu.Unlock()
	if err != nil {
		http.Error(w, "could not create login link", http.StatusInternalServerError)
		return
	}

	link := fmt.Sprintf("%s/api/auth/verify?token=%s", publicBaseURL(), token)
	log.Printf("magic link for %s: %s", email, link)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "sent",
		"email":      email,
		"magic_link": link,
		"token":      token, // TODO(M5): remove once email delivery exists
		"expires_in": int(magicLinkTTL.Seconds()),
	})
}

// handleAuthVerify consumes a magic link, creates the account if needed, opens a
// session, and either redirects (browser ?redirect=) or returns the session token.
func handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}

	mu.Lock()
	var email string
	err := db.QueryRow(`SELECT email FROM magic_links
		WHERE token = ? AND used = 0 AND expires_at > datetime('now')`, token).Scan(&email)
	if err == nil {
		_, err = db.Exec(`UPDATE magic_links SET used = 1 WHERE token = ?`, token)
	}
	mu.Unlock()
	if err != nil {
		http.Error(w, "invalid or expired login link", http.StatusUnauthorized)
		return
	}

	userID, created, err := ensureUser(email)
	if err != nil {
		http.Error(w, "could not establish session", http.StatusInternalServerError)
		return
	}
	sessionToken, err := createSession(userID)
	if err != nil {
		http.Error(w, "could not establish session", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, sessionToken)
	log.Printf("login: %s (%s) session issued", userID, email)

	if redirect := r.URL.Query().Get("redirect"); redirect != "" {
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "ok",
		"user_id":       userID,
		"email":         email,
		"created":       created,
		"session_token": sessionToken,
		"credits":       balance(userID),
	})
}

// handleAuthMe returns the authenticated user and their credit balance.
func handleAuthMe(w http.ResponseWriter, r *http.Request) {
	userID := userFromRequest(r)
	if userID == "" {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	var email string
	if err := db.QueryRow(`SELECT email FROM users WHERE id = ?`, userID).Scan(&email); err != nil {
		http.Error(w, "unknown user", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id": userID,
		"email":   email,
		"credits": balance(userID),
	})
}

// handleAuthLogout revokes the current session and clears the cookie.
func handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		mu.Lock()
		_, _ = db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
		mu.Unlock()
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		mu.Lock()
		_, _ = db.Exec(`DELETE FROM sessions WHERE token = ?`, c.Value)
		mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}
