package main

import (
	"net/http"
	"time"
)

// adminEmail is the only account allowed to view admin pages. Hard-coded
// because there is exactly one operator and no plan to expand the role.
const adminEmail = "james67@gmail.com"

func isAdmin(u *currentUser) bool {
	return u != nil && u.Email == adminEmail
}

// requireAdmin layers on top of requireAuth: a non-admin authenticated user
// gets a 404 (not 403) so the page is indistinguishable from a missing route.
func requireAdmin(next http.Handler) http.Handler {
	return requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdmin(userFrom(r)) {
			handle404(w, r)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

type viewAdminUserRow struct {
	ID            int64
	Email         string
	Name          string
	Created       string
	LastLogin     string
	WorkoutCount  int64
}

type viewAdminUsers struct {
	UserName  string
	ThemeMode string
	Users     []viewAdminUserRow
}

func handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	rows, err := queries.ListUsersForAdmin(r.Context())
	if err != nil {
		serverError(w, "admin: list users", err)
		return
	}
	out := make([]viewAdminUserRow, 0, len(rows))
	for _, u := range rows {
		out = append(out, viewAdminUserRow{
			ID:           u.ID,
			Email:        u.Email,
			Name:         u.Name,
			Created:      formatAdminTimestamp(u.CreatedAt),
			LastLogin:    formatAdminTimestamp(u.LastLoginAt),
			WorkoutCount: u.WorkoutCount,
		})
	}
	renderHTML(w, "admin_users.html", viewAdminUsers{
		UserName:  user.Name,
		ThemeMode: themeFromRequest(r),
		Users:     out,
	})
}

// formatAdminTimestamp parses an RFC3339 string (created_at / last_login_at
// are stored that way) and renders it in the configured app timezone. Falls
// back to the raw value so a malformed row stays visible rather than blank.
func formatAdminTimestamp(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.In(appLocation).Format("2 Jan 2006 15:04")
}
