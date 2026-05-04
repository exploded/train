-- ASCII-only comments (sqlc/SQLite quirk).

-- =============================================================================
-- USERS
-- =============================================================================

-- name: GetUserByGoogleSub :one
SELECT id, google_sub, email, name, created_at, last_login_at
FROM users WHERE google_sub = ?;

-- name: GetUserByID :one
SELECT id, google_sub, email, name, created_at, last_login_at
FROM users WHERE id = ?;

-- name: CreateUser :exec
INSERT INTO users (google_sub, email, name, created_at, last_login_at)
VALUES (?, ?, ?, ?, ?);

-- name: GetLastUser :one
SELECT id, google_sub, email, name, created_at, last_login_at
FROM users WHERE id = (SELECT MAX(id) FROM users);

-- name: UpdateUserLastLogin :exec
UPDATE users SET last_login_at = ? WHERE id = ?;

-- =============================================================================
-- SESSIONS
-- =============================================================================

-- name: CreateSession :exec
INSERT INTO sessions (token, user_id, expires_at, created_at)
VALUES (?, ?, ?, ?);

-- name: GetSessionUser :one
SELECT u.id, u.google_sub, u.email, u.name, u.created_at, u.last_login_at,
       s.expires_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token = ? AND s.expires_at > ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= ?;

-- =============================================================================
-- EXERCISES
-- =============================================================================

-- name: ListExercises :many
-- Global order. Used by progression code where order does not matter.
SELECT id, slug, name, kind, default_sets, default_reps, default_weight_kg, sort_order
FROM exercises ORDER BY sort_order;

-- name: ListExercisesForUser :many
-- Per-user order: rows in user_exercise_sort_order override exercises.sort_order.
-- Exercises without a per-user row fall back to the seeded default.
SELECT e.id, e.slug, e.name, e.kind, e.default_sets, e.default_reps, e.default_weight_kg, e.sort_order
FROM exercises e
LEFT JOIN user_exercise_sort_order uso
    ON uso.exercise_id = e.id AND uso.user_id = ?
ORDER BY COALESCE(uso.sort_order, e.sort_order), e.id;

-- name: GetExerciseByID :one
SELECT id, slug, name, kind, default_sets, default_reps, default_weight_kg, sort_order
FROM exercises WHERE id = ?;

-- name: UpsertExercise :exec
-- sort_order is intentionally only set on initial INSERT so that user
-- reordering on the settings page is not clobbered by the seed on restart.
INSERT INTO exercises (slug, name, kind, default_sets, default_reps, default_weight_kg, sort_order)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(slug) DO UPDATE SET
    name = excluded.name,
    kind = excluded.kind,
    default_sets = excluded.default_sets,
    default_reps = excluded.default_reps;

-- name: ClearUserExerciseSortOrder :exec
DELETE FROM user_exercise_sort_order WHERE user_id = ?;

-- name: UpsertUserExerciseSortOrder :exec
INSERT INTO user_exercise_sort_order (user_id, exercise_id, sort_order)
VALUES (?, ?, ?)
ON CONFLICT(user_id, exercise_id) DO UPDATE SET
    sort_order = excluded.sort_order;

-- =============================================================================
-- USER EXERCISE WEIGHT
-- =============================================================================

-- name: GetUserExerciseWeight :one
SELECT user_id, exercise_id, weight_kg, success_streak, updated_at
FROM user_exercise_weight
WHERE user_id = ? AND exercise_id = ?;

-- name: UpsertUserExerciseWeight :exec
INSERT INTO user_exercise_weight (user_id, exercise_id, weight_kg, success_streak, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(user_id, exercise_id) DO UPDATE SET
    weight_kg = excluded.weight_kg,
    success_streak = excluded.success_streak,
    updated_at = excluded.updated_at;

-- name: UpdateUserExerciseWeightOnly :exec
UPDATE user_exercise_weight
SET weight_kg = ?, updated_at = ?
WHERE user_id = ? AND exercise_id = ?;

-- =============================================================================
-- USER EXERCISE HIDDEN
-- =============================================================================

-- name: ListHiddenExerciseIDs :many
SELECT exercise_id FROM user_exercise_hidden WHERE user_id = ?;

-- name: HideExercise :exec
INSERT INTO user_exercise_hidden (user_id, exercise_id)
VALUES (?, ?)
ON CONFLICT(user_id, exercise_id) DO NOTHING;

-- name: UnhideExercise :exec
DELETE FROM user_exercise_hidden WHERE user_id = ? AND exercise_id = ?;

-- =============================================================================
-- WORKOUTS
-- =============================================================================

-- name: GetWorkoutByDate :one
SELECT id, user_id, workout_date, created_at, completed_at
FROM workouts
WHERE user_id = ? AND workout_date = ?;

-- name: GetWorkoutByID :one
SELECT id, user_id, workout_date, created_at, completed_at
FROM workouts WHERE id = ?;

-- name: CreateWorkout :exec
INSERT INTO workouts (user_id, workout_date, created_at) VALUES (?, ?, ?);

-- name: GetLastWorkoutBefore :one
SELECT id, user_id, workout_date, created_at, completed_at
FROM workouts
WHERE user_id = ? AND workout_date < ?
ORDER BY workout_date DESC LIMIT 1;

-- name: FinishWorkout :exec
UPDATE workouts SET completed_at = ? WHERE id = ? AND user_id = ?;

-- name: UnfinishWorkout :exec
UPDATE workouts SET completed_at = NULL WHERE id = ? AND user_id = ? AND workout_date = ?;

-- name: DeleteWorkout :exec
-- user_id check is defence in depth; handler already verifies ownership.
-- ON DELETE CASCADE on sets and walking_sessions cleans up child rows.
DELETE FROM workouts WHERE id = ? AND user_id = ?;

-- name: ListUserWorkoutsPaged :many
SELECT id, user_id, workout_date, created_at, completed_at
FROM workouts
WHERE user_id = ?
ORDER BY workout_date DESC
LIMIT ? OFFSET ?;

-- name: CountUserWorkouts :one
SELECT COUNT(*) FROM workouts WHERE user_id = ?;

-- name: ListUserWorkoutsSince :many
SELECT id, workout_date, completed_at
FROM workouts
WHERE user_id = ? AND workout_date >= ?
ORDER BY workout_date DESC;

-- name: ListUserExerciseWeights :many
SELECT exercise_id, weight_kg, success_streak
FROM user_exercise_weight WHERE user_id = ?;

-- name: ListSetsForWorkoutIDs :many
SELECT id, workout_id, exercise_id, set_index, target_reps, actual_reps, weight_kg
FROM sets
WHERE workout_id IN (sqlc.slice('workout_ids'))
ORDER BY workout_id DESC, exercise_id, set_index;

-- name: ListWeightHistoryForExercise :many
SELECT w.id AS workout_id, w.workout_date,
       s.weight_kg, s.set_index, s.target_reps, s.actual_reps
FROM sets s
JOIN workouts w ON w.id = s.workout_id
WHERE w.user_id = ? AND s.exercise_id = ?
ORDER BY w.workout_date DESC, s.set_index
LIMIT ?;

-- =============================================================================
-- SETS
-- =============================================================================

-- name: ListSetsForWorkout :many
SELECT id, workout_id, exercise_id, set_index, target_reps, actual_reps, weight_kg
FROM sets WHERE workout_id = ?
ORDER BY exercise_id, set_index;

-- name: ListSetsForWorkoutExercise :many
SELECT id, workout_id, exercise_id, set_index, target_reps, actual_reps, weight_kg
FROM sets WHERE workout_id = ? AND exercise_id = ?
ORDER BY set_index;

-- name: GetSet :one
SELECT id, workout_id, exercise_id, set_index, target_reps, actual_reps, weight_kg
FROM sets WHERE id = ?;

-- name: GetLastSetIDInWorkout :one
-- Returns the id of the very last set in user display order: among all sets
-- in the workout, the one whose exercise has the largest effective sort_order
-- (per-user override or global default), tie-broken by exercise id, and
-- within that exercise the largest set_index. Used to suppress the rest
-- timer on the final tap of the workout.
SELECT s.id
FROM sets s
JOIN exercises e ON e.id = s.exercise_id
LEFT JOIN user_exercise_sort_order uso
    ON uso.exercise_id = s.exercise_id AND uso.user_id = sqlc.arg(user_id)
WHERE s.workout_id = sqlc.arg(workout_id)
ORDER BY COALESCE(uso.sort_order, e.sort_order) DESC,
         e.id DESC,
         s.set_index DESC
LIMIT 1;

-- name: CountUntappedSetsInWorkout :one
-- Counts sets that have never been tapped (actual_reps IS NULL). Used to
-- decide whether a tap was the "last to complete" - i.e. the user filled in
-- the final outstanding set, perhaps after going back to one they skipped.
SELECT COUNT(*) FROM sets WHERE workout_id = ? AND actual_reps IS NULL;

-- name: CreateSet :exec
INSERT INTO sets (workout_id, exercise_id, set_index, target_reps, actual_reps, weight_kg)
VALUES (?, ?, ?, ?, NULL, ?);

-- name: UpdateSetActualReps :exec
-- user_id check is defence in depth; handlers already verify ownership.
UPDATE sets SET actual_reps = ?
WHERE sets.id = ?
  AND sets.workout_id IN (SELECT w.id FROM workouts w WHERE w.user_id = ?);

-- name: UpdateSetsWeightForExercise :exec
UPDATE sets SET weight_kg = ?
WHERE sets.workout_id = ? AND sets.exercise_id = ?
  AND sets.workout_id IN (SELECT w.id FROM workouts w WHERE w.user_id = ?);

-- =============================================================================
-- WALKING SESSIONS
-- =============================================================================

-- name: GetWalkingSession :one
SELECT workout_id, duration_min, speed_x10, incline_x10
FROM walking_sessions WHERE workout_id = ?;

-- name: UpsertWalkingSession :exec
-- The SELECT yields zero rows (and therefore writes nothing) if the workout
-- does not belong to user_id. Handlers already check ownership; this is
-- defence in depth.
INSERT INTO walking_sessions (workout_id, duration_min, speed_x10, incline_x10)
SELECT w.id,
       sqlc.arg(duration_min),
       sqlc.arg(speed_x10),
       sqlc.arg(incline_x10)
FROM workouts w
WHERE w.id = sqlc.arg(workout_id) AND w.user_id = sqlc.arg(user_id)
ON CONFLICT(workout_id) DO UPDATE SET
    duration_min = excluded.duration_min,
    speed_x10    = excluded.speed_x10,
    incline_x10  = excluded.incline_x10;

-- name: GetLastUserWalkingSession :one
SELECT ws.workout_id, ws.duration_min, ws.speed_x10, ws.incline_x10
FROM walking_sessions ws
JOIN workouts w ON w.id = ws.workout_id
WHERE w.user_id = ? AND w.workout_date < ?
ORDER BY w.workout_date DESC
LIMIT 1;

-- name: ListUserWalkingHistory :many
SELECT w.id AS workout_id, w.workout_date,
       ws.duration_min, ws.speed_x10, ws.incline_x10,
       s.actual_reps
FROM walking_sessions ws
JOIN workouts w ON w.id = ws.workout_id
LEFT JOIN sets s ON s.workout_id = w.id AND s.exercise_id = ?
WHERE w.user_id = ?
ORDER BY w.workout_date DESC
LIMIT ?;
