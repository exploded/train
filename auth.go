package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"train/db"
)

const (
	sessionCookieName = "train_session"
	sessionTTL        = 30 * 24 * time.Hour
	stateCookieName   = "train_oauth_state"
	stateTTL          = 10 * time.Minute
)

type oauthConfig struct {
	enabled    bool
	cfg        *oauth2.Config
	verifier   *oidc.IDTokenVerifier
	sessionKey []byte
}

func initOAuth(ctx context.Context) error {
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	redirect := os.Getenv("OAUTH_REDIRECT_URL")
	keyHex := os.Getenv("SESSION_KEY")

	if keyHex == "" {
		return errors.New("SESSION_KEY not set")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) < 16 {
		return errors.New("SESSION_KEY must be hex with at least 16 bytes (32 hex chars)")
	}
	oauthCfg.sessionKey = key

	if clientID == "" || clientSecret == "" || redirect == "" {
		oauthCfg.enabled = false
		return errors.New("GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, OAUTH_REDIRECT_URL must be set")
	}

	provider, err := oidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return fmt.Errorf("oidc provider: %w", err)
	}
	oauthCfg.cfg = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirect,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
	oauthCfg.verifier = provider.Verifier(&oidc.Config{ClientID: clientID})
	oauthCfg.enabled = true
	return nil
}

// signState returns a token of the form <nonce>.<hex(hmac(nonce))>. The
// nonce gets stashed in a short-lived cookie; on callback, we recompute the
// MAC and compare against both the cookie and the state query param.
func signState(nonce string) string {
	mac := hmac.New(sha256.New, oauthCfg.sessionKey)
	mac.Write([]byte(nonce))
	return nonce + "." + hex.EncodeToString(mac.Sum(nil))
}

func verifyState(token string) (string, bool) {
	i := strings.LastIndexByte(token, '.')
	if i < 0 {
		return "", false
	}
	nonce := token[:i]
	if !hmac.Equal([]byte(signState(nonce)), []byte(token)) {
		return "", false
	}
	return nonce, true
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	renderHTML(w, "login.html", struct{ ThemeMode string }{ThemeMode: themeFromRequest(r)})
}

func handlePrivacyPage(w http.ResponseWriter, r *http.Request) {
	renderHTML(w, "privacy.html", nil)
}

func handleTermsPage(w http.ResponseWriter, r *http.Request) {
	renderHTML(w, "terms.html", nil)
}

func handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !oauthCfg.enabled {
		http.Error(w, "OAuth not configured", http.StatusServiceUnavailable)
		return
	}

	nonce, err := randHex(16)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	state := signState(nonce)

	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    nonce,
		Path:     "/auth/google/callback",
		Expires:  time.Now().Add(stateTTL),
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, oauthCfg.cfg.AuthCodeURL(state), http.StatusSeeOther)
}

func handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !oauthCfg.enabled {
		http.Error(w, "OAuth not configured", http.StatusServiceUnavailable)
		return
	}

	stateParam := r.URL.Query().Get("state")
	nonce, ok := verifyState(stateParam)
	if !ok {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	c, err := r.Cookie(stateCookieName)
	if err != nil || c.Value != nonce {
		http.Error(w, "state cookie mismatch", http.StatusBadRequest)
		return
	}
	// Clear the state cookie immediately.
	http.SetCookie(w, &http.Cookie{Name: stateCookieName, Value: "", Path: "/auth/google/callback", MaxAge: -1})

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	tok, err := oauthCfg.cfg.Exchange(r.Context(), code)
	if err != nil {
		slog.Error("oauth exchange", "error", err)
		http.Error(w, "exchange failed", http.StatusBadGateway)
		return
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		http.Error(w, "missing id_token", http.StatusBadGateway)
		return
	}
	idTok, err := oauthCfg.verifier.Verify(r.Context(), rawID)
	if err != nil {
		slog.Error("verify id_token", "error", err)
		http.Error(w, "invalid id_token", http.StatusBadGateway)
		return
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Sub           string `json:"sub"`
	}
	if err := idTok.Claims(&claims); err != nil {
		http.Error(w, "bad claims", http.StatusBadGateway)
		return
	}
	if claims.Sub == "" || !claims.EmailVerified {
		http.Error(w, "unverified Google account", http.StatusForbidden)
		return
	}

	user, err := upsertUser(r.Context(), claims.Sub, claims.Email, claims.Name)
	if err != nil {
		serverError(w, "upsert user", err)
		return
	}
	if err := issueSessionCookie(r.Context(), w, user.ID, isSecureRequest(r)); err != nil {
		serverError(w, "issue session", err)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// cleanupExpiredSessions purges expired session rows once an hour. Without
// this they accumulate forever — login issues a new row each time and only
// `/auth/logout` ever deletes anything.
func cleanupExpiredSessions() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := queries.DeleteExpiredSessions(ctx, time.Now().UTC().Format(time.RFC3339)); err != nil {
			slog.Warn("cleanup expired sessions", "error", err)
		}
		cancel()
		<-t.C
	}
}

func handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		// Log delete failures so a DB outage doesn't silently leave a live
		// session row behind after the cookie has been cleared client-side.
		if err := queries.DeleteSession(r.Context(), c.Value); err != nil {
			slog.Error("logout: delete session", "error", err)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func upsertUser(ctx context.Context, sub, email, name string) (db.User, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	user, err := queries.GetUserByGoogleSub(ctx, sub)
	if err == nil {
		_ = queries.UpdateUserLastLogin(ctx, db.UpdateUserLastLoginParams{
			LastLoginAt: now, ID: user.ID,
		})
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return db.User{}, err
	}
	if err := queries.CreateUser(ctx, db.CreateUserParams{
		GoogleSub: sub, Email: email, Name: name,
		CreatedAt: now, LastLoginAt: now,
	}); err != nil {
		return db.User{}, err
	}
	return queries.GetLastUser(ctx)
}

func issueSessionCookie(ctx context.Context, w http.ResponseWriter, userID int64, secure bool) error {
	token, err := randHex(32)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := queries.CreateSession(ctx, db.CreateSessionParams{
		Token:     token,
		UserID:    userID,
		ExpiresAt: now.Add(sessionTTL).Format(time.RFC3339),
		CreatedAt: now.Format(time.RFC3339),
	}); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  now.Add(sessionTTL),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

type ctxKey int

const userCtxKey ctxKey = 0

type currentUser struct {
	ID    int64
	Email string
	Name  string
}

func userFrom(r *http.Request) *currentUser {
	if v, ok := r.Context().Value(userCtxKey).(*currentUser); ok {
		return v
	}
	return nil
}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil || c.Value == "" {
			redirectToLogin(w, r)
			return
		}
		row, err := queries.GetSessionUser(r.Context(), db.GetSessionUserParams{
			Token:     c.Value,
			ExpiresAt: time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			redirectToLogin(w, r)
			return
		}
		u := &currentUser{ID: row.ID, Email: row.Email, Name: row.Name}
		ctx := context.WithValue(r.Context(), userCtxKey, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	// HTMX requests can't follow a 302 to a different origin; signal via header.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
