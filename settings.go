package main

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"train/db"
)

const (
	restSoundCookieName = "rest_sound"
	restSoundTTL        = 365 * 24 * time.Hour

	restSoundOn  = "on"
	restSoundOff = "off"
)

func restSoundFromRequest(r *http.Request) string {
	c, err := r.Cookie(restSoundCookieName)
	if err != nil {
		return restSoundOn
	}
	if c.Value == restSoundOff {
		return restSoundOff
	}
	return restSoundOn
}

func setRestSoundCookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     restSoundCookieName,
		Value:    value,
		Path:     "/",
		Expires:  time.Now().Add(restSoundTTL),
		HttpOnly: false,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// =============================================================================
// Settings page: per-user exercise visibility
// =============================================================================

type viewSettingsRow struct {
	ID     int64
	Name   string
	Hidden bool
}

type viewSettings struct {
	UserName  string
	ThemeMode string
	RestSound string
	Exercises []viewSettingsRow
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	exercises, err := queries.ListExercisesForUser(r.Context(), user.ID)
	if err != nil {
		serverError(w, "settings: list exercises", err)
		return
	}
	hidden, err := hiddenExerciseSet(r.Context(), queries, user.ID)
	if err != nil {
		serverError(w, "settings: list hidden", err)
		return
	}
	vs := viewSettings{
		UserName:  user.Name,
		ThemeMode: themeFromRequest(r),
		RestSound: restSoundFromRequest(r),
	}
	for _, ex := range exercises {
		vs.Exercises = append(vs.Exercises, viewSettingsRow{
			ID: ex.ID, Name: ex.Name, Hidden: hidden[ex.ID],
		})
	}
	renderHTML(w, "settings.html", vs)
}

// handleSettingsToggle flips visibility for one exercise. Renders a single
// row partial back so HTMX can swap it in place.
func handleSettingsToggle(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	exID, ok := pathInt64(w, r, "id", "exercise id")
	if !ok {
		return
	}
	ex, err := queries.GetExerciseByID(r.Context(), exID)
	if err != nil {
		http.Error(w, "exercise not found", http.StatusNotFound)
		return
	}

	hidden, err := hiddenExerciseSet(r.Context(), queries, user.ID)
	if err != nil {
		serverError(w, "settings toggle: list hidden", err)
		return
	}
	wasHidden := hidden[exID]
	if wasHidden {
		if err := queries.UnhideExercise(r.Context(), db.UnhideExerciseParams{
			UserID: user.ID, ExerciseID: exID,
		}); err != nil {
			serverError(w, "settings toggle: unhide", err)
			return
		}
	} else {
		if err := queries.HideExercise(r.Context(), db.HideExerciseParams{
			UserID: user.ID, ExerciseID: exID,
		}); err != nil {
			serverError(w, "settings toggle: hide", err)
			return
		}
	}

	renderHTML(w, "settings_row.html", viewSettingsRow{ID: ex.ID, Name: ex.Name, Hidden: !wasHidden})
}

// handleSettingsReorder accepts a comma-separated list of exercise IDs in
// the desired display order and writes them to user_exercise_sort_order
// scoped to the calling user. Exercises absent from the list fall back to
// the global default order on read.
func handleSettingsReorder(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	raw := r.PostForm.Get("order")
	if raw == "" {
		http.Error(w, "missing order", http.StatusBadRequest)
		return
	}
	parts := strings.Split(raw, ",")
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil {
			http.Error(w, "bad id in order", http.StatusBadRequest)
			return
		}
		ids = append(ids, id)
	}

	tx, err := store.BeginTx(r.Context(), nil)
	if err != nil {
		serverError(w, "reorder begin tx", err)
		return
	}
	defer tx.Rollback()
	qtx := queries.WithTx(tx)
	if err := qtx.ClearUserExerciseSortOrder(r.Context(), user.ID); err != nil {
		serverError(w, "reorder clear", err)
		return
	}
	for i, id := range ids {
		if err := qtx.UpsertUserExerciseSortOrder(r.Context(), db.UpsertUserExerciseSortOrderParams{
			UserID: user.ID, ExerciseID: id, SortOrder: int64(i + 1),
		}); err != nil {
			slog.Error("reorder upsert", "error", err, "id", id)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		serverError(w, "reorder commit", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSettingsRestSoundToggle flips the rest-timer sound preference cookie
// and renders the updated switch back so HTMX can swap it in place.
func handleSettingsRestSoundToggle(w http.ResponseWriter, r *http.Request) {
	next := restSoundOn
	if restSoundFromRequest(r) == restSoundOn {
		next = restSoundOff
	}
	setRestSoundCookie(w, r, next)
	renderHTML(w, "rest_sound_toggle.html", next == restSoundOn)
}
