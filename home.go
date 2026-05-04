package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"train/db"
)

const (
	historyPageSize  = 30
	homeRecentLimit  = 5
	homeActivityDays = 30
)

// =============================================================================
// View models
// =============================================================================

type viewHome struct {
	UserName  string
	ThemeMode string // "light" | "dark" | "auto"
	Today     viewTodayCard
	Stats     viewStats
	Weights   []viewWeightRow
	Activity  []viewActivityCell // oldest -> newest, len == homeActivityDays
	Recent    []viewHistoryRow   // capped at homeRecentLimit
	HasMore   bool               // whether to show "View all sessions" link
}

type viewStats struct {
	ThisWeek      int // completed workouts in last 7 days incl. today
	ThisMonth     int // completed workouts in current calendar month
	SessionStreak int // consecutive completed workouts back from most recent
}

type viewWeightRow struct {
	Slug       string
	Name       string // shortened display name
	WeightDisp string // formatted kg, e.g. "70" or "12.5"
	Streak     int    // 0..len(Dots)
	Dots       []bool // length successStreakBumpAt; true = filled
	NextBump   bool   // streak >= successStreakBumpAt-1 (one more session to bump)
	Manual     bool   // walking, dumbbell_curls
}

type viewActivityCell struct {
	Date  string // YYYY-MM-DD
	State string // "done" | "partial" | "rest" | "today"
}

type viewTodayCard struct {
	WorkoutID int64
	Date      string
	State     string // "not_started" | "in_progress" | "completed"
	StateText string // human label
	Tapped    int    // sets with actual_reps != NULL
	Total     int    // total sets
}

type viewHistoryRow struct {
	WorkoutID int64
	Date      string
	Completed bool
	Pills     []viewHistoryPill
}

type viewHistoryPill struct {
	Name   string // shortened exercise name
	Weight string // formatted kg, blank for cardio
	Status string // "ok" | "partial" | "untapped"
}

// =============================================================================
// Helpers
// =============================================================================

var shortExerciseNames = map[string]string{
	"bench_press":    "Bench",
	"overhead_press": "OHP",
	"barbell_row":    "Row",
	"deadlift":       "DL",
	"face_pull":      "Face",
	"lat_pulldown":   "Lat",
	"walking":        "Walk",
	"dumbbell_curls": "Curl",
}

func shortName(slug, fallback string) string {
	if s, ok := shortExerciseNames[slug]; ok {
		return s
	}
	return fallback
}

func pillStatus(sets []db.Set) string {
	anyTapped := false
	for _, s := range sets {
		if !s.ActualReps.Valid {
			continue
		}
		anyTapped = true
		if s.ActualReps.Int64 != s.TargetReps {
			return "partial"
		}
	}
	if !anyTapped {
		return "untapped"
	}
	return "ok"
}

// =============================================================================
// Homepage
// =============================================================================

func handleHome(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	ctx := r.Context()

	exercises, err := queries.ListExercisesForUser(ctx, user.ID)
	if err != nil {
		serverError(w, "home: list exercises", err)
		return
	}

	vh := viewHome{
		UserName:  user.Name,
		ThemeMode: themeFromRequest(r),
	}

	// Today's card.
	today := todayInAppTZ()
	wk, err := queries.GetWorkoutByDate(ctx, db.GetWorkoutByDateParams{
		UserID: user.ID, WorkoutDate: today,
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		vh.Today = viewTodayCard{
			Date:      niceDate(today),
			State:     "not_started",
			StateText: "Not started",
		}
	case err != nil:
		serverError(w, "home: today workout", err)
		return
	default:
		card := viewTodayCard{WorkoutID: wk.ID, Date: niceDate(wk.WorkoutDate)}
		sets, _ := queries.ListSetsForWorkout(ctx, wk.ID)
		card.Total = len(sets)
		for _, s := range sets {
			if s.ActualReps.Valid {
				card.Tapped++
			}
		}
		switch {
		case wk.CompletedAt.Valid:
			card.State, card.StateText = "completed", "Completed"
		case card.Tapped == 0:
			card.State, card.StateText = "not_started", "Not started"
		default:
			card.State, card.StateText = "in_progress", fmt.Sprintf("%d / %d sets", card.Tapped, card.Total)
		}
		vh.Today = card
	}

	// Last 30 days of workouts powers both Stats and Activity grid.
	since := time.Now().In(appLocation).AddDate(0, 0, -(homeActivityDays - 1)).Format("2006-01-02")
	recentWks, err := queries.ListUserWorkoutsSince(ctx, db.ListUserWorkoutsSinceParams{
		UserID: user.ID, WorkoutDate: since,
	})
	if err != nil {
		serverError(w, "home: workouts since", err)
		return
	}
	vh.Stats = buildStats(ctx, user.ID, today, recentWks)
	vh.Activity = buildActivity(today, recentWks)

	// Working weights tile.
	vh.Weights, err = buildWeights(ctx, user.ID, exercises)
	if err != nil {
		serverError(w, "home: build weights", err)
		return
	}

	// Recent: up to homeRecentLimit history rows. Pull a wider window so we
	// can show 5 even if some recent workouts had no tapped sets (those get
	// filtered by loadHistoryRows).
	rows, hasMore, err := loadHistoryRows(ctx, user.ID, exercises, 0, homeRecentLimit*2)
	if err != nil {
		serverError(w, "home: load history", err)
		return
	}
	if len(rows) > homeRecentLimit {
		rows = rows[:homeRecentLimit]
		hasMore = true
	}
	vh.Recent = rows
	vh.HasMore = hasMore || len(rows) == homeRecentLimit
	renderHTML(w, "home.html", vh)
}

// buildStats summarizes the user's recent workout cadence. recentWks must be
// the window-of-30-days slice (DESC by date) returned by ListUserWorkoutsSince.
func buildStats(_ context.Context, _ int64, today string, recentWks []db.ListUserWorkoutsSinceRow) viewStats {
	t, err := time.ParseInLocation("2006-01-02", today, appLocation)
	if err != nil {
		return viewStats{}
	}
	weekFloor := t.AddDate(0, 0, -6).Format("2006-01-02") // last 7 days incl. today
	monthPrefix := t.Format("2006-01")                    // YYYY-MM

	var s viewStats
	for _, w := range recentWks {
		if !w.CompletedAt.Valid {
			continue
		}
		if w.WorkoutDate >= weekFloor {
			s.ThisWeek++
		}
		if strings.HasPrefix(w.WorkoutDate, monthPrefix) {
			s.ThisMonth++
		}
	}

	// Session streak: consecutive completed workouts back from the most
	// recent. Today's incomplete workout doesn't break the streak.
	for _, w := range recentWks {
		if w.WorkoutDate == today && !w.CompletedAt.Valid {
			continue
		}
		if w.CompletedAt.Valid {
			s.SessionStreak++
		} else {
			break
		}
	}
	return s
}

// buildActivity returns 30 cells, oldest -> newest, ending today.
func buildActivity(today string, recentWks []db.ListUserWorkoutsSinceRow) []viewActivityCell {
	byDate := make(map[string]db.ListUserWorkoutsSinceRow, len(recentWks))
	for _, w := range recentWks {
		byDate[w.WorkoutDate] = w
	}
	t, err := time.ParseInLocation("2006-01-02", today, appLocation)
	if err != nil {
		return nil
	}
	cells := make([]viewActivityCell, 0, homeActivityDays)
	for i := homeActivityDays - 1; i >= 0; i-- {
		date := t.AddDate(0, 0, -i).Format("2006-01-02")
		state := "rest"
		if w, ok := byDate[date]; ok {
			if w.CompletedAt.Valid {
				state = "done"
			} else {
				state = "partial"
			}
		}
		if date == today && state == "rest" {
			state = "today"
		}
		cells = append(cells, viewActivityCell{Date: date, State: state})
	}
	return cells
}

// buildWeights produces one row per visible exercise, with current working
// weight and streak progress toward the next 2.5 kg bump.
func buildWeights(ctx context.Context, userID int64, exercises []db.Exercise) ([]viewWeightRow, error) {
	hidden, err := hiddenExerciseSet(ctx, queries, userID)
	if err != nil {
		return nil, err
	}
	uewRows, err := queries.ListUserExerciseWeights(ctx, userID)
	if err != nil {
		return nil, err
	}
	uewByEx := make(map[int64]db.ListUserExerciseWeightsRow, len(uewRows))
	for _, u := range uewRows {
		uewByEx[u.ExerciseID] = u
	}

	out := make([]viewWeightRow, 0, len(exercises))
	for _, ex := range exercises {
		if hidden[ex.ID] {
			continue
		}
		if ex.Kind == "cardio" {
			continue // walking shows nothing useful here; skip to keep the tile lifting-focused
		}
		uew, hasUEW := uewByEx[ex.ID]
		weightKg := ex.DefaultWeightKg
		streak := 0
		if hasUEW {
			weightKg = uew.WeightKg
			streak = int(uew.SuccessStreak)
		}
		manual := !exerciseAutoProgresses(ex)
		row := viewWeightRow{
			Slug:       ex.Slug,
			Name:       shortName(ex.Slug, ex.Name),
			WeightDisp: formatKg(weightKg),
			Manual:     manual,
		}
		if !manual {
			if streak > successStreakBumpAt {
				streak = successStreakBumpAt
			}
			row.Streak = streak
			row.Dots = make([]bool, successStreakBumpAt)
			for i := 0; i < streak; i++ {
				row.Dots[i] = true
			}
			row.NextBump = streak >= successStreakBumpAt-1
		}
		out = append(out, row)
	}
	return out, nil
}

// loadHistoryRows fetches one page of past workouts (date DESC) with their
// per-exercise compact summary. Skips today's workout from the list (it's
// already shown in the Today card).
func loadHistoryRows(ctx context.Context, userID int64, exercises []db.Exercise, offset, limit int) ([]viewHistoryRow, bool, error) {
	// Pull one extra row to detect HasMore cheaply.
	wks, err := queries.ListUserWorkoutsPaged(ctx, db.ListUserWorkoutsPagedParams{
		UserID: userID,
		Limit:  int64(limit + 1),
		Offset: int64(offset),
	})
	if err != nil {
		return nil, false, err
	}

	hasMore := len(wks) > limit
	if hasMore {
		wks = wks[:limit]
	}

	// Filter out today's workout from the history list (it's the Today card).
	today := todayInAppTZ()
	filtered := wks[:0]
	for _, w := range wks {
		if offset == 0 && w.WorkoutDate == today {
			continue
		}
		filtered = append(filtered, w)
	}
	wks = filtered

	if len(wks) == 0 {
		return nil, hasMore, nil
	}

	ids := make([]int64, len(wks))
	for i, w := range wks {
		ids[i] = w.ID
	}
	allSets, err := queries.ListSetsForWorkoutIDs(ctx, ids)
	if err != nil {
		return nil, false, err
	}
	// Group sets by (workoutID, exerciseID).
	type key struct{ w, e int64 }
	bucket := make(map[key][]db.Set, len(allSets))
	for _, s := range allSets {
		k := key{s.WorkoutID, s.ExerciseID}
		bucket[k] = append(bucket[k], s)
	}

	rows := make([]viewHistoryRow, 0, len(wks))
	for _, w := range wks {
		row := viewHistoryRow{
			WorkoutID: w.ID,
			Date:      niceDate(w.WorkoutDate),
			Completed: w.CompletedAt.Valid,
		}
		for _, ex := range exercises {
			sets := bucket[key{w.ID, ex.ID}]
			if len(sets) == 0 {
				continue
			}
			status := pillStatus(sets)
			if status == "untapped" {
				continue // exercise was scheduled but the user didn't do it
			}
			pill := viewHistoryPill{
				Name:   shortName(ex.Slug, ex.Name),
				Status: status,
			}
			if ex.Kind != "cardio" {
				pill.Weight = formatKg(sets[0].WeightKg)
			}
			row.Pills = append(row.Pills, pill)
		}
		if len(row.Pills) == 0 {
			continue // entire workout had no taps; don't show an empty row
		}
		rows = append(rows, row)
	}
	return rows, hasMore, nil
}

// =============================================================================
// History "Load more" (HTMX)
// =============================================================================

func handleHistoryMore(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	exercises, err := queries.ListExercisesForUser(r.Context(), user.ID)
	if err != nil {
		serverError(w, "history more: list exercises", err)
		return
	}
	rows, hasMore, err := loadHistoryRows(r.Context(), user.ID, exercises, offset, historyPageSize)
	if err != nil {
		serverError(w, "history more: load rows", err)
		return
	}

	data := struct {
		History    []viewHistoryRow
		NextOffset int
		HasMore    bool
	}{rows, offset + historyPageSize, hasMore}
	renderHTML(w, "history_more.html", data)
}

// =============================================================================
// Expand a history row (HTMX)
// =============================================================================

type viewExpandedRow struct {
	WorkoutID int64
	Exercises []viewExercise
}

// handleWorkoutDelete removes a workout (and its sets / walking session via
// CASCADE) for the current user. Two callers:
//   - History row delete (HTMX): empty 200 lets the caller swap the row's
//     outerHTML with nothing so it disappears from the list.
//   - Workout-page delete (plain form): redirect home so the user doesn't
//     land on an empty 200 page, and so navigating back to /workout starts a
//     fresh workout instead of resurrecting the just-deleted one.
func handleWorkoutDelete(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	wkID, ok := pathInt64(w, r, "id", "workout id")
	if !ok {
		return
	}
	wk, err := queries.GetWorkoutByID(r.Context(), wkID)
	if err != nil || wk.UserID != user.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := queries.DeleteWorkout(r.Context(), db.DeleteWorkoutParams{
		ID: wk.ID, UserID: user.ID,
	}); err != nil {
		serverError(w, "delete workout", err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleWorkoutExpand(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	wkID, ok := pathInt64(w, r, "id", "workout id")
	if !ok {
		return
	}
	wk, err := queries.GetWorkoutByID(r.Context(), wkID)
	if err != nil || wk.UserID != user.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	exercises, err := queries.ListExercisesForUser(r.Context(), user.ID)
	if err != nil {
		serverError(w, "expand: list exercises", err)
		return
	}
	allSets, err := queries.ListSetsForWorkout(r.Context(), wk.ID)
	if err != nil {
		serverError(w, "expand: list sets", err)
		return
	}
	setsByEx := make(map[int64][]db.Set)
	for _, s := range allSets {
		setsByEx[s.ExerciseID] = append(setsByEx[s.ExerciseID], s)
	}

	row := viewExpandedRow{WorkoutID: wk.ID}
	for _, ex := range exercises {
		sets := setsByEx[ex.ID]
		if len(sets) == 0 {
			continue
		}
		if pillStatus(sets) == "untapped" {
			continue
		}
		var walking *db.WalkingSession
		if ex.Kind == "cardio" {
			ws := loadOrDefaultWalking(r.Context(), wk.ID)
			walking = &ws
		}
		ve := buildExerciseView(ex, sets, walking)
		ve.Locked = true // history rows are always read-only
		for i := range ve.Sets {
			ve.Sets[i].Locked = true
		}
		row.Exercises = append(row.Exercises, ve)
	}
	renderHTML(w, "history_expanded.html", row)
}

// =============================================================================
// History full-page (View all sessions)
// =============================================================================

type viewHistory struct {
	UserName   string
	ThemeMode  string
	History    []viewHistoryRow
	NextOffset int
	HasMore    bool
}

func handleHistoryPage(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	exercises, err := queries.ListExercisesForUser(r.Context(), user.ID)
	if err != nil {
		serverError(w, "history page: list exercises", err)
		return
	}
	rows, hasMore, err := loadHistoryRows(r.Context(), user.ID, exercises, 0, historyPageSize)
	if err != nil {
		serverError(w, "history page: load rows", err)
		return
	}
	vh := viewHistory{
		UserName:   user.Name,
		ThemeMode:  themeFromRequest(r),
		History:    rows,
		HasMore:    hasMore,
		NextOffset: historyPageSize,
	}
	renderHTML(w, "history.html", vh)
}
