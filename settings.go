package main

import (
	"database/sql"
	"fmt"
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

// customExerciseKinds are the kinds a user is allowed to pick when creating
// a custom exercise. Cardio is excluded because it depends on the
// walking_sessions table; supporting that would need a separate flow.
var customExerciseKinds = []string{"barbell", "machine", "dumbbell"}

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
	ID       int64
	Name     string
	Hidden   bool
	IsCustom bool // user owns this exercise: show rename + delete affordances
}

type viewSettings struct {
	UserName    string
	ThemeMode   string
	RestSound   string
	Exercises   []viewSettingsRow
	CustomKinds []string
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
		UserName:    user.Name,
		ThemeMode:   themeFromRequest(r),
		RestSound:   restSoundFromRequest(r),
		CustomKinds: customExerciseKinds,
	}
	for _, ex := range exercises {
		vs.Exercises = append(vs.Exercises, viewSettingsRow{
			ID:       ex.ID,
			Name:     ex.Name,
			Hidden:   hidden[ex.ID],
			IsCustom: ex.CreatedByUserID.Valid && ex.CreatedByUserID.Int64 == user.ID,
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
	if !exerciseVisibleTo(ex, user.ID) {
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

	renderHTML(w, "settings_row.html", viewSettingsRow{
		ID:       ex.ID,
		Name:     ex.Name,
		Hidden:   !wasHidden,
		IsCustom: ex.CreatedByUserID.Valid && ex.CreatedByUserID.Int64 == user.ID,
	})
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

// =============================================================================
// Custom exercises (private to creator)
// =============================================================================

// exerciseVisibleTo returns true if the given exercise is either seeded
// (created_by_user_id IS NULL) or owned by the given user. Soft-deleted
// rows are not visible.
func exerciseVisibleTo(ex db.Exercise, userID int64) bool {
	if ex.DeletedAt.Valid {
		return false
	}
	if !ex.CreatedByUserID.Valid {
		return true // seeded: visible to everyone
	}
	return ex.CreatedByUserID.Int64 == userID
}

// slugifyExerciseName produces a lowercase ASCII identifier from a
// user-supplied exercise name, suitable for use as part of a slug. Non-alnum
// runs collapse to a single underscore. Empty result is replaced with "ex".
func slugifyExerciseName(name string) string {
	var b strings.Builder
	prevUnderscore := true // suppress leading underscore
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "_")
	if out == "" {
		return "ex"
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

func parseIntField(form, key string, min, max int64) (int64, error) {
	raw := strings.TrimSpace(form)
	if raw == "" {
		return 0, fmt.Errorf("%s required", key)
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", key)
	}
	if v < min || v > max {
		return 0, fmt.Errorf("%s out of range", key)
	}
	return v, nil
}

func parseFloatField(form, key string, min, max float64) (float64, error) {
	raw := strings.TrimSpace(form)
	if raw == "" {
		return 0, fmt.Errorf("%s required", key)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", key)
	}
	if v < min || v > max {
		return 0, fmt.Errorf("%s out of range", key)
	}
	return v, nil
}

func handleSettingsCustomCreate(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.PostForm.Get("name"))
	if n := len(name); n < 1 || n > 40 {
		http.Error(w, "name must be 1-40 characters", http.StatusBadRequest)
		return
	}
	kind := r.PostForm.Get("kind")
	allowed := false
	for _, k := range customExerciseKinds {
		if k == kind {
			allowed = true
			break
		}
	}
	if !allowed {
		http.Error(w, "kind not allowed", http.StatusBadRequest)
		return
	}
	defaultSets, err := parseIntField(r.PostForm.Get("default_sets"), "sets", 1, 10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defaultReps, err := parseIntField(r.PostForm.Get("default_reps"), "reps", 1, 30)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defaultWeight, err := parseFloatField(r.PostForm.Get("default_weight_kg"), "weight", 0, 500)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	autoProgress := int64(0)
	if r.PostForm.Get("auto_progress") != "" {
		autoProgress = 1
	}

	slug := fmt.Sprintf("u%d_%s_%d", user.ID, slugifyExerciseName(name), time.Now().UnixNano())

	tx, err := store.BeginTx(r.Context(), nil)
	if err != nil {
		serverError(w, "custom create begin tx", err)
		return
	}
	defer tx.Rollback()
	qtx := queries.WithTx(tx)

	maxSort, err := qtx.MaxExerciseSortOrder(r.Context())
	if err != nil {
		serverError(w, "custom create: max sort", err)
		return
	}

	if _, err := qtx.CreateCustomExercise(r.Context(), db.CreateCustomExerciseParams{
		Slug:            slug,
		Name:            name,
		Kind:            kind,
		DefaultSets:     defaultSets,
		DefaultReps:     defaultReps,
		DefaultWeightKg: defaultWeight,
		SortOrder:       maxSort + 1,
		CreatedByUserID: sql.NullInt64{Int64: user.ID, Valid: true},
		AutoProgress:    autoProgress,
	}); err != nil {
		serverError(w, "custom create insert", err)
		return
	}

	if err := tx.Commit(); err != nil {
		serverError(w, "custom create commit", err)
		return
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func handleSettingsCustomRename(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	exID, ok := pathInt64(w, r, "id", "exercise id")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if n := len(name); n < 1 || n > 40 {
		http.Error(w, "name must be 1-40 characters", http.StatusBadRequest)
		return
	}

	ex, err := queries.GetExerciseByID(r.Context(), exID)
	if err != nil || !ex.CreatedByUserID.Valid || ex.CreatedByUserID.Int64 != user.ID || ex.DeletedAt.Valid {
		http.Error(w, "exercise not found", http.StatusNotFound)
		return
	}

	if err := queries.RenameCustomExercise(r.Context(), db.RenameCustomExerciseParams{
		Name:            name,
		ID:              exID,
		CreatedByUserID: sql.NullInt64{Int64: user.ID, Valid: true},
	}); err != nil {
		serverError(w, "custom rename", err)
		return
	}

	hidden, err := hiddenExerciseSet(r.Context(), queries, user.ID)
	if err != nil {
		serverError(w, "custom rename: list hidden", err)
		return
	}
	renderHTML(w, "settings_row.html", viewSettingsRow{
		ID: exID, Name: name, Hidden: hidden[exID], IsCustom: true,
	})
}

func handleSettingsCustomDelete(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	exID, ok := pathInt64(w, r, "id", "exercise id")
	if !ok {
		return
	}

	ex, err := queries.GetExerciseByID(r.Context(), exID)
	if err != nil || !ex.CreatedByUserID.Valid || ex.CreatedByUserID.Int64 != user.ID || ex.DeletedAt.Valid {
		http.Error(w, "exercise not found", http.StatusNotFound)
		return
	}

	if err := queries.SoftDeleteCustomExercise(r.Context(), db.SoftDeleteCustomExerciseParams{
		DeletedAt:       sql.NullString{String: time.Now().UTC().Format(time.RFC3339), Valid: true},
		ID:              exID,
		CreatedByUserID: sql.NullInt64{Int64: user.ID, Valid: true},
	}); err != nil {
		serverError(w, "custom delete", err)
		return
	}
	w.WriteHeader(http.StatusOK) // empty body; HTMX swaps the row out
}
