-- Train app schema. SQLite. ASCII-only comments throughout this file
-- (sqlc v1.30 corrupts generated queries when comments contain non-ASCII
-- characters such as em-dashes).

CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    google_sub    TEXT NOT NULL UNIQUE,
    email         TEXT NOT NULL,
    name          TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    last_login_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    token       TEXT PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);

CREATE TABLE IF NOT EXISTS exercises (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    slug              TEXT NOT NULL UNIQUE,
    name              TEXT NOT NULL,
    -- kind: 'barbell','machine','dumbbell','cardio'
    kind              TEXT NOT NULL,
    default_sets      INTEGER NOT NULL,
    default_reps      INTEGER NOT NULL,
    default_weight_kg REAL NOT NULL,
    sort_order        INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS user_exercise_weight (
    user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    exercise_id    INTEGER NOT NULL REFERENCES exercises(id) ON DELETE CASCADE,
    weight_kg      REAL NOT NULL,
    -- consecutive fully-completed sessions; bumps weight at >= 5 then resets
    success_streak INTEGER NOT NULL DEFAULT 0,
    updated_at     TEXT NOT NULL,
    PRIMARY KEY (user_id, exercise_id)
);

-- Per-user hide list. A row here means the exercise is excluded from new
-- workouts for that user. Past workouts are unaffected.
CREATE TABLE IF NOT EXISTS user_exercise_hidden (
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    exercise_id INTEGER NOT NULL REFERENCES exercises(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, exercise_id)
);

CREATE TABLE IF NOT EXISTS workouts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- YYYY-MM-DD in the configured app timezone (default Australia/Sydney)
    workout_date TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    -- non-NULL once the user has hit Finish; locks the workout from edits
    completed_at TEXT,
    UNIQUE(user_id, workout_date)
);
CREATE INDEX IF NOT EXISTS idx_workouts_user_date ON workouts(user_id, workout_date DESC);

CREATE TABLE IF NOT EXISTS sets (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    workout_id   INTEGER NOT NULL REFERENCES workouts(id) ON DELETE CASCADE,
    exercise_id  INTEGER NOT NULL REFERENCES exercises(id) ON DELETE CASCADE,
    set_index    INTEGER NOT NULL,
    target_reps  INTEGER NOT NULL,
    -- NULL = not yet tapped (initial blue/white state)
    actual_reps  INTEGER,
    weight_kg    REAL NOT NULL,
    UNIQUE(workout_id, exercise_id, set_index)
);
CREATE INDEX IF NOT EXISTS idx_sets_workout_exercise ON sets(workout_id, exercise_id);

-- One row per workout that includes the walking exercise. Stores the
-- adjustable cardio parameters. speed_x10 and incline_x10 are stored as
-- integers to avoid floating-point drift across +/- adjustments
-- (speed: 0..100 = 0.0..10.0 kph in 0.1 steps; incline: in 0.5 steps so
-- valid values are multiples of 5).
CREATE TABLE IF NOT EXISTS walking_sessions (
    workout_id   INTEGER PRIMARY KEY REFERENCES workouts(id) ON DELETE CASCADE,
    duration_min INTEGER NOT NULL,
    speed_x10    INTEGER NOT NULL,
    incline_x10  INTEGER NOT NULL
);
