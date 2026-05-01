package main

import (
	"net/http"
	"os"
	"time"
)

const (
	themeCookieName = "theme"
	themeTTL        = 365 * 24 * time.Hour

	themeAuto  = "auto"
	themeLight = "light"
	themeDark  = "dark"
)

func themeFromRequest(r *http.Request) string {
	c, err := r.Cookie(themeCookieName)
	if err != nil {
		return themeAuto
	}
	switch c.Value {
	case themeLight, themeDark, themeAuto:
		return c.Value
	}
	return themeAuto
}

func handleSetTheme(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	mode := r.PostForm.Get("mode")
	switch mode {
	case themeLight, themeDark, themeAuto:
	default:
		http.Error(w, "invalid mode", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     themeCookieName,
		Value:    mode,
		Path:     "/",
		Expires:  time.Now().Add(themeTTL),
		HttpOnly: false,
		Secure:   r.TLS != nil || os.Getenv("PROD") == "True",
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
