package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"train/db"
)

// =============================================================================
// Finish / Unfinish workout
// =============================================================================

func handleWorkoutFinish(w http.ResponseWriter, r *http.Request) {
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
	if !wk.CompletedAt.Valid {
		now := time.Now().UTC().Format(time.RFC3339)
		if err := queries.FinishWorkout(r.Context(), db.FinishWorkoutParams{
			CompletedAt: sql.NullString{String: now, Valid: true},
			ID:          wkID,
			UserID:      user.ID,
		}); err != nil {
			serverError(w, "finish workout", err)
			return
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleWorkoutUnfinish(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	wkID, ok := pathInt64(w, r, "id", "workout id")
	if !ok {
		return
	}
	if err := queries.UnfinishWorkout(r.Context(), db.UnfinishWorkoutParams{
		ID:          wkID,
		UserID:      user.ID,
		WorkoutDate: todayInAppTZ(),
	}); err != nil {
		serverError(w, "unfinish workout", err)
		return
	}
	http.Redirect(w, r, "/workout", http.StatusSeeOther)
}

// =============================================================================
// View model
// =============================================================================

type viewSet struct {
	ID         int64
	SetIndex   int
	TargetReps int
	ActualReps int  // 0 if not yet tapped (use Tapped to disambiguate)
	Tapped     bool // true once user has tapped at least once
	Successful bool // ActualReps == TargetReps
	Locked     bool // parent workout finished; render display-only
}

type viewExercise struct {
	ID         int64
	Slug       string
	Name       string
	Kind       string
	Sets       []viewSet
	WeightKg   float64
	WeightDisp string // "60" or "12.5" — no trailing zeros
	IsBarbell  bool
	IsCardio   bool
	Plates     []Plate // for barbell only
	// Cardio (Walking) only:
	WalkingDone bool
	DurationMin int    // duration in minutes
	SpeedDisp   string // "5.5"
	InclineDisp string // "2.0"
	// Display string for header
	SetsXReps string // formatted "<sets>x<reps>", empty for cardio
	Detail    string // for cardio history: "15 min @ 5.5 kph / 2.0 incline"
	// Locked = parent workout is finished; circles are display-only.
	Locked bool
}

type viewWorkout struct {
	ID        int64
	Date      string // "Friday, 1 May" formatted in app TZ
	UserName  string
	ThemeMode string
	Exercises []viewExercise
	Locked    bool   // workout has completed_at set
	IsToday   bool   // workout_date == today
}

func todayInAppTZ() string {
	return time.Now().In(appLocation).Format("2006-01-02")
}

func niceDate(yyyymmdd string) string {
	t, err := time.ParseInLocation("2006-01-02", yyyymmdd, appLocation)
	if err != nil {
		return yyyymmdd
	}
	return t.Format("Monday, 2 Jan")
}

func formatKg(kg float64) string {
	if kg == float64(int(kg)) {
		return strconv.Itoa(int(kg))
	}
	return strconv.FormatFloat(kg, 'f', -1, 64)
}

func clampInt64(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// =============================================================================
// Walking helpers (cardio adjustable parameters)
// =============================================================================

const (
	walkingDefaultDurationMin = 15
	walkingDefaultSpeedX10    = 55 // 5.5 kph
	walkingDefaultInclineX10  = 20 // 2.0
	walkingMaxSpeedX10        = 100
	walkingSpeedStepX10       = 1  // 0.1 kph
	walkingInclineStepX10     = 5  // 0.5
	walkingDurationStep       = 1  // 1 minute
	walkingMaxInclineX10      = 200
	walkingMaxDurationMin     = 240
)

// formatX10 turns an int*10 magnitude into a one-decimal display string
// ("55" -> "5.5"). Used for walking speed and incline.
func formatX10(v int64) string {
	whole := v / 10
	frac := v % 10
	if frac < 0 {
		frac = -frac
	}
	return strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(frac, 10)
}

// loadOrDefaultWalking returns the workout's walking session, falling back
// to hardcoded defaults if the row hasn't been created yet (e.g. workouts
// that predate the walking_sessions table). The returned struct is always
// safe to read but is not persisted by this call.
func loadOrDefaultWalking(ctx context.Context, workoutID int64) db.WalkingSession {
	ws, err := queries.GetWalkingSession(ctx, workoutID)
	if err == nil {
		return ws
	}
	return db.WalkingSession{
		WorkoutID:   workoutID,
		DurationMin: walkingDefaultDurationMin,
		SpeedX10:    walkingDefaultSpeedX10,
		InclineX10:  walkingDefaultInclineX10,
	}
}

// seedWalkingForNewWorkout creates today's walking_sessions row using the
// user's most recent prior session as the template, or hardcoded defaults
// if there is no prior session.
func seedWalkingForNewWorkout(ctx context.Context, q *db.Queries, userID int64, workoutID int64, today string) error {
	prev, err := q.GetLastUserWalkingSession(ctx, db.GetLastUserWalkingSessionParams{
		UserID: userID, WorkoutDate: today,
	})
	duration := int64(walkingDefaultDurationMin)
	speed := int64(walkingDefaultSpeedX10)
	incline := int64(walkingDefaultInclineX10)
	if err == nil {
		duration, speed, incline = prev.DurationMin, prev.SpeedX10, prev.InclineX10
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return q.UpsertWalkingSession(ctx, db.UpsertWalkingSessionParams{
		WorkoutID:   workoutID,
		DurationMin: duration,
		SpeedX10:    speed,
		InclineX10:  incline,
	})
}

// =============================================================================
// Workout page
// =============================================================================

func handleWorkout(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	if user == nil {
		redirectToLogin(w, r)
		return
	}

	today := todayInAppTZ()
	workout, err := getOrCreateTodaysWorkout(r.Context(), user.ID, today)
	if err != nil {
		serverError(w, "get/create workout", err)
		return
	}

	vw, err := buildWorkoutView(r.Context(), user, workout)
	if err != nil {
		serverError(w, "build workout view", err)
		return
	}
	vw.ThemeMode = themeFromRequest(r)
	renderHTML(w, "workout.html", vw)
}

// getOrCreateTodaysWorkout: idempotent. On first call of the day, runs the
// auto-progression check using the previous workout's results, then creates
// the workout row plus its sets at each user's current per-exercise weight.
// The create path runs in a single transaction so a partial failure can't
// leave a workout shell with missing sets.
func getOrCreateTodaysWorkout(ctx context.Context, userID int64, today string) (db.Workout, error) {
	wk, err := queries.GetWorkoutByDate(ctx, db.GetWorkoutByDateParams{
		UserID: userID, WorkoutDate: today,
	})
	if err == nil {
		return wk, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return db.Workout{}, err
	}

	tx, err := store.BeginTx(ctx, nil)
	if err != nil {
		return db.Workout{}, err
	}
	defer tx.Rollback()
	q := queries.WithTx(tx)

	if err := runProgressionForUser(ctx, q, userID, today); err != nil {
		return db.Workout{}, fmt.Errorf("progression: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if err := q.CreateWorkout(ctx, db.CreateWorkoutParams{
		UserID: userID, WorkoutDate: today, CreatedAt: now,
	}); err != nil {
		return db.Workout{}, err
	}
	wk, err = q.GetWorkoutByDate(ctx, db.GetWorkoutByDateParams{
		UserID: userID, WorkoutDate: today,
	})
	if err != nil {
		return db.Workout{}, err
	}

	exercises, err := q.ListExercises(ctx)
	if err != nil {
		return db.Workout{}, err
	}
	hidden, err := hiddenExerciseSet(ctx, q, userID)
	if err != nil {
		return db.Workout{}, err
	}
	for _, ex := range exercises {
		if hidden[ex.ID] {
			continue
		}
		weight, err := userWeightFor(ctx, q, userID, ex)
		if err != nil {
			return db.Workout{}, err
		}
		for i := int64(1); i <= ex.DefaultSets; i++ {
			if err := q.CreateSet(ctx, db.CreateSetParams{
				WorkoutID:  wk.ID,
				ExerciseID: ex.ID,
				SetIndex:   i,
				TargetReps: ex.DefaultReps,
				WeightKg:   weight,
			}); err != nil {
				return db.Workout{}, err
			}
		}
		if ex.Kind == "cardio" {
			if err := seedWalkingForNewWorkout(ctx, q, userID, wk.ID, today); err != nil {
				return db.Workout{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return db.Workout{}, err
	}
	return wk, nil
}

// hiddenExerciseSet returns the user's hide list as a set keyed by exercise_id.
func hiddenExerciseSet(ctx context.Context, q *db.Queries, userID int64) (map[int64]bool, error) {
	ids, err := q.ListHiddenExerciseIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m, nil
}

func anySetTapped(sets []db.Set) bool {
	for _, s := range sets {
		if s.ActualReps.Valid {
			return true
		}
	}
	return false
}

// userWeightFor returns the user's current working weight for an exercise,
// creating the user_exercise_weight row at the exercise default if missing.
func userWeightFor(ctx context.Context, q *db.Queries, userID int64, ex db.Exercise) (float64, error) {
	uew, err := q.GetUserExerciseWeight(ctx, db.GetUserExerciseWeightParams{
		UserID: userID, ExerciseID: ex.ID,
	})
	if err == nil {
		return uew.WeightKg, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := q.UpsertUserExerciseWeight(ctx, db.UpsertUserExerciseWeightParams{
		UserID: userID, ExerciseID: ex.ID,
		WeightKg: ex.DefaultWeightKg, SuccessStreak: 0,
		UpdatedAt: now,
	}); err != nil {
		return 0, err
	}
	return ex.DefaultWeightKg, nil
}

func buildWorkoutView(ctx context.Context, user *currentUser, wk db.Workout) (viewWorkout, error) {
	exercises, err := queries.ListExercises(ctx)
	if err != nil {
		return viewWorkout{}, err
	}
	allSets, err := queries.ListSetsForWorkout(ctx, wk.ID)
	if err != nil {
		return viewWorkout{}, err
	}
	setsByExID := make(map[int64][]db.Set)
	for _, s := range allSets {
		setsByExID[s.ExerciseID] = append(setsByExID[s.ExerciseID], s)
	}
	hidden, err := hiddenExerciseSet(ctx, queries, user.ID)
	if err != nil {
		return viewWorkout{}, err
	}

	vw := viewWorkout{
		ID:       wk.ID,
		Date:     niceDate(wk.WorkoutDate),
		UserName: user.Name,
		Locked:   wk.CompletedAt.Valid,
		IsToday:  wk.WorkoutDate == todayInAppTZ(),
	}
	for _, ex := range exercises {
		sets := setsByExID[ex.ID]
		if len(sets) == 0 {
			continue
		}
		// If the user hid this exercise after today's workout was already
		// created, drop it from the live view. Sets remain in the DB so any
		// already-tapped reps still surface in history.
		if hidden[ex.ID] && !anySetTapped(sets) {
			continue
		}
		var walking *db.WalkingSession
		if ex.Kind == "cardio" {
			ws := loadOrDefaultWalking(ctx, wk.ID)
			walking = &ws
		}
		ve := buildExerciseView(ex, sets, walking)
		ve.Locked = vw.Locked
		for i := range ve.Sets {
			ve.Sets[i].Locked = vw.Locked
		}
		vw.Exercises = append(vw.Exercises, ve)
	}
	return vw, nil
}

func buildExerciseView(ex db.Exercise, sets []db.Set, walking *db.WalkingSession) viewExercise {
	ve := viewExercise{
		ID:        ex.ID,
		Slug:      ex.Slug,
		Name:      ex.Name,
		Kind:      ex.Kind,
		IsBarbell: ex.Kind == "barbell",
		IsCardio:  ex.Kind == "cardio",
	}
	if len(sets) > 0 {
		ve.WeightKg = sets[0].WeightKg
	} else {
		ve.WeightKg = ex.DefaultWeightKg
	}
	ve.WeightDisp = formatKg(ve.WeightKg)

	if ve.IsBarbell {
		ve.Plates = platesForSide(ve.WeightKg)
	}

	if ve.IsCardio {
		ws := db.WalkingSession{
			DurationMin: walkingDefaultDurationMin,
			SpeedX10:    walkingDefaultSpeedX10,
			InclineX10:  walkingDefaultInclineX10,
		}
		if walking != nil {
			ws = *walking
		}
		ve.DurationMin = int(ws.DurationMin)
		ve.SpeedDisp = formatX10(ws.SpeedX10)
		ve.InclineDisp = formatX10(ws.InclineX10)
		ve.Detail = fmt.Sprintf("%d min @ %s kph / %s incline",
			ve.DurationMin, ve.SpeedDisp, ve.InclineDisp)
		// Walking: a single set; "Done" if its actual_reps was set.
		if len(sets) > 0 && sets[0].ActualReps.Valid && sets[0].ActualReps.Int64 > 0 {
			ve.WalkingDone = true
		}
		return ve
	}

	for _, s := range sets {
		vs := toViewSet(s)
		vs.Locked = ve.Locked
		ve.Sets = append(ve.Sets, vs)
	}
	if len(sets) > 0 {
		ve.SetsXReps = fmt.Sprintf("%dx%d", len(sets), int(sets[0].TargetReps))
	}
	return ve
}

func toViewSet(s db.Set) viewSet {
	v := viewSet{
		ID:         s.ID,
		SetIndex:   int(s.SetIndex),
		TargetReps: int(s.TargetReps),
	}
	if s.ActualReps.Valid {
		v.Tapped = true
		v.ActualReps = int(s.ActualReps.Int64)
		v.Successful = v.ActualReps == v.TargetReps
	}
	return v
}

// =============================================================================
// Lock helper - shared by all mutation endpoints
// =============================================================================

// editableTodayWorkout returns today's workout for the user iff it exists
// and is not finished. On lock violation, writes 409 + HX-Trigger and
// returns ok=false so the caller just returns.
func editableTodayWorkout(w http.ResponseWriter, r *http.Request, userID int64) (db.Workout, bool) {
	wk, err := queries.GetWorkoutByDate(r.Context(), db.GetWorkoutByDateParams{
		UserID: userID, WorkoutDate: todayInAppTZ(),
	})
	if err != nil {
		http.Error(w, "no workout today", http.StatusBadRequest)
		return db.Workout{}, false
	}
	if wk.CompletedAt.Valid {
		w.Header().Set("HX-Trigger", "workoutLocked")
		http.Error(w, "workout finished", http.StatusConflict)
		return db.Workout{}, false
	}
	return wk, true
}

// =============================================================================
// Tap a rep circle
// =============================================================================

func handleSetTap(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	id, ok := pathInt64(w, r, "id", "set id")
	if !ok {
		return
	}

	s, err := queries.GetSet(r.Context(), id)
	if err != nil {
		http.Error(w, "set not found", http.StatusNotFound)
		return
	}

	wk, ok := editableTodayWorkout(w, r, user.ID)
	if !ok {
		return
	}
	if s.WorkoutID != wk.ID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// State machine:
	//   not yet tapped (NULL)  -> target_reps (success)
	//   N > 0                   -> N - 1
	//   0                       -> target_reps (wraps around)
	var next int64
	wasUntapped := !s.ActualReps.Valid
	switch {
	case wasUntapped:
		next = s.TargetReps
	case s.ActualReps.Int64 > 0:
		next = s.ActualReps.Int64 - 1
	default: // == 0
		next = s.TargetReps
	}

	if err := queries.UpdateSetActualReps(r.Context(), db.UpdateSetActualRepsParams{
		ActualReps: sql.NullInt64{Int64: next, Valid: true}, ID: id,
	}); err != nil {
		serverError(w, "update set", err)
		return
	}

	// First tap of a set fires the rest timer on the client.
	if wasUntapped {
		w.Header().Set("HX-Trigger", "startRestTimer")
	}

	view := toViewSet(db.Set{
		ID:         s.ID,
		SetIndex:   s.SetIndex,
		TargetReps: s.TargetReps,
		ActualReps: sql.NullInt64{Int64: next, Valid: true},
	})
	renderHTML(w, "circle.html", view)
}

// =============================================================================
// Walking done toggle
// =============================================================================

func handleWalkingDone(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	exID, ok := pathInt64(w, r, "id", "exercise id")
	if !ok {
		return
	}
	ex, err := queries.GetExerciseByID(r.Context(), exID)
	if err != nil || ex.Kind != "cardio" {
		http.Error(w, "not a cardio exercise", http.StatusBadRequest)
		return
	}
	wk, ok := editableTodayWorkout(w, r, user.ID)
	if !ok {
		return
	}
	sets, err := queries.ListSetsForWorkoutExercise(r.Context(), db.ListSetsForWorkoutExerciseParams{
		WorkoutID: wk.ID, ExerciseID: exID,
	})
	if err != nil || len(sets) == 0 {
		serverError(w, "walking done: list sets", err)
		return
	}
	s := sets[0]
	var next sql.NullInt64
	done := s.ActualReps.Valid && s.ActualReps.Int64 > 0
	if done {
		next = sql.NullInt64{Int64: 0, Valid: true}
	} else {
		next = sql.NullInt64{Int64: 1, Valid: true}
	}
	if err := queries.UpdateSetActualReps(r.Context(), db.UpdateSetActualRepsParams{
		ActualReps: next, ID: s.ID,
	}); err != nil {
		serverError(w, "walking done: update set", err)
		return
	}

	walking := loadOrDefaultWalking(r.Context(), s.WorkoutID)
	ve := buildExerciseView(ex, []db.Set{{
		ID: s.ID, WorkoutID: s.WorkoutID, ExerciseID: s.ExerciseID,
		SetIndex: s.SetIndex, TargetReps: s.TargetReps,
		ActualReps: next, WeightKg: s.WeightKg,
	}}, &walking)
	renderHTML(w, "exercise.html", ve)
}

// =============================================================================
// Weight edit (+/- 2.5 kg)
// =============================================================================

func handleWeightChange(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	exID, ok := pathInt64(w, r, "id", "exercise id")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	delta, err := strconv.ParseFloat(r.FormValue("delta"), 64)
	if err != nil {
		http.Error(w, "bad delta", http.StatusBadRequest)
		return
	}
	if delta != weightIncrementKg && delta != weightDecrementKg {
		http.Error(w, "delta must be +/- 2.5", http.StatusBadRequest)
		return
	}

	ex, err := queries.GetExerciseByID(r.Context(), exID)
	if err != nil {
		http.Error(w, "exercise not found", http.StatusNotFound)
		return
	}
	if ex.Kind == "cardio" {
		http.Error(w, "cardio has no weight", http.StatusBadRequest)
		return
	}

	wk, ok := editableTodayWorkout(w, r, user.ID)
	if !ok {
		return
	}

	current, err := userWeightFor(r.Context(), queries, user.ID, ex)
	if err != nil {
		serverError(w, "weight change: lookup", err)
		return
	}
	next := current + delta

	// Floor depends on exercise: barbell can't go below the bar (20 kg);
	// machine/dumbbell floors at 0.
	floor := 0.0
	if ex.Kind == "barbell" {
		floor = barWeightKg
	}
	if next < floor {
		next = floor
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if err := queries.UpdateUserExerciseWeightOnly(r.Context(), db.UpdateUserExerciseWeightOnlyParams{
		WeightKg: next, UpdatedAt: now,
		UserID: user.ID, ExerciseID: exID,
	}); err != nil {
		serverError(w, "weight change: update", err)
		return
	}

	// Propagate to today's not-yet-tapped sets so the user sees the new weight.
	_ = queries.UpdateSetsWeightForExercise(r.Context(), db.UpdateSetsWeightForExerciseParams{
		WeightKg: next, WorkoutID: wk.ID, ExerciseID: exID,
	})

	// Re-render the entire exercise card so circles + barbell + header all sync.
	sets, _ := queries.ListSetsForWorkoutExercise(r.Context(), db.ListSetsForWorkoutExerciseParams{
		WorkoutID: wk.ID, ExerciseID: exID,
	})
	ve := buildExerciseView(ex, sets, nil)
	renderHTML(w, "exercise.html", ve)
}

// =============================================================================
// Walking adjust (duration / speed / incline +/-)
// =============================================================================

func handleWalkingAdjust(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	exID, ok := pathInt64(w, r, "id", "exercise id")
	if !ok {
		return
	}
	ex, err := queries.GetExerciseByID(r.Context(), exID)
	if err != nil || ex.Kind != "cardio" {
		http.Error(w, "not a cardio exercise", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	field := r.FormValue("field")
	dir, err := strconv.Atoi(r.FormValue("dir"))
	if err != nil || (dir != 1 && dir != -1) {
		http.Error(w, "bad dir", http.StatusBadRequest)
		return
	}

	wk, ok := editableTodayWorkout(w, r, user.ID)
	if !ok {
		return
	}

	ws := loadOrDefaultWalking(r.Context(), wk.ID)
	switch field {
	case "duration":
		ws.DurationMin = clampInt64(ws.DurationMin+int64(dir)*walkingDurationStep, 0, walkingMaxDurationMin)
	case "speed":
		ws.SpeedX10 = clampInt64(ws.SpeedX10+int64(dir)*walkingSpeedStepX10, 0, walkingMaxSpeedX10)
	case "incline":
		ws.InclineX10 = clampInt64(ws.InclineX10+int64(dir)*walkingInclineStepX10, 0, walkingMaxInclineX10)
	default:
		http.Error(w, "bad field", http.StatusBadRequest)
		return
	}

	if err := queries.UpsertWalkingSession(r.Context(), db.UpsertWalkingSessionParams{
		WorkoutID:   wk.ID,
		DurationMin: ws.DurationMin,
		SpeedX10:    ws.SpeedX10,
		InclineX10:  ws.InclineX10,
	}); err != nil {
		serverError(w, "upsert walking session", err)
		return
	}

	sets, _ := queries.ListSetsForWorkoutExercise(r.Context(), db.ListSetsForWorkoutExerciseParams{
		WorkoutID: wk.ID, ExerciseID: exID,
	})
	ve := buildExerciseView(ex, sets, &ws)
	renderHTML(w, "exercise.html", ve)
}
