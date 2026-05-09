package db

import (
	"context"
	"database/sql"
)

// SeededExercise is the canonical fixed exercise list. Modifying these here
// will propagate to existing databases on next startup via UpsertExercise
// (default_weight_kg only affects new users; per-user weights live in
// user_exercise_weight and are not touched by seeding).
//
// AutoProgress is only set on initial INSERT (UpsertExercise does not update
// it on conflict). For pre-existing rows we run SetSeededAutoProgress below
// to keep walking and dumbbell_curls aligned even after the column is added
// via migration.
var SeededExercises = []UpsertExerciseParams{
	{Slug: "squat", Name: "Squat", Kind: "barbell", DefaultSets: 5, DefaultReps: 5, DefaultWeightKg: 60, SortOrder: 1, AutoProgress: 1},
	{Slug: "bench_press", Name: "Bench Press", Kind: "barbell", DefaultSets: 5, DefaultReps: 5, DefaultWeightKg: 60, SortOrder: 2, AutoProgress: 1},
	{Slug: "overhead_press", Name: "Overhead Press", Kind: "barbell", DefaultSets: 5, DefaultReps: 5, DefaultWeightKg: 40, SortOrder: 3, AutoProgress: 1},
	{Slug: "barbell_row", Name: "Barbell Row", Kind: "barbell", DefaultSets: 3, DefaultReps: 5, DefaultWeightKg: 70, SortOrder: 4, AutoProgress: 1},
	{Slug: "deadlift", Name: "Deadlift", Kind: "barbell", DefaultSets: 3, DefaultReps: 5, DefaultWeightKg: 70, SortOrder: 5, AutoProgress: 1},
	{Slug: "face_pull", Name: "Face Pull", Kind: "machine", DefaultSets: 5, DefaultReps: 5, DefaultWeightKg: 30, SortOrder: 6, AutoProgress: 1},
	{Slug: "lat_pulldown", Name: "Lat Pulldown", Kind: "machine", DefaultSets: 5, DefaultReps: 5, DefaultWeightKg: 65, SortOrder: 7, AutoProgress: 1},
	{Slug: "walking", Name: "Walking", Kind: "cardio", DefaultSets: 1, DefaultReps: 1, DefaultWeightKg: 0, SortOrder: 8, AutoProgress: 0},
	{Slug: "dumbbell_curls", Name: "Dumbbell Curls", Kind: "dumbbell", DefaultSets: 5, DefaultReps: 5, DefaultWeightKg: 15, SortOrder: 9, AutoProgress: 0},
	{Slug: "dips", Name: "Dips", Kind: "dumbbell", DefaultSets: 3, DefaultReps: 10, DefaultWeightKg: 0, SortOrder: 10, AutoProgress: 1},
	{Slug: "pullups", Name: "Pullups", Kind: "dumbbell", DefaultSets: 3, DefaultReps: 5, DefaultWeightKg: 0, SortOrder: 11, AutoProgress: 1},
}

func SeedExercises(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := New(tx)
	for _, ex := range SeededExercises {
		if err := q.UpsertExercise(ctx, ex); err != nil {
			return err
		}
		if err := q.SetSeededAutoProgress(ctx, SetSeededAutoProgressParams{
			AutoProgress: ex.AutoProgress, Slug: ex.Slug,
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}
