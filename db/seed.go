package db

import "context"

// SeededExercise is the canonical fixed exercise list. Modifying these here
// will propagate to existing databases on next startup via UpsertExercise
// (default_weight_kg only affects new users; per-user weights live in
// user_exercise_weight and are not touched by seeding).
var SeededExercises = []UpsertExerciseParams{
	{Slug: "squat", Name: "Squat", Kind: "barbell", DefaultSets: 5, DefaultReps: 5, DefaultWeightKg: 60, SortOrder: 1},
	{Slug: "bench_press", Name: "Bench Press", Kind: "barbell", DefaultSets: 5, DefaultReps: 5, DefaultWeightKg: 60, SortOrder: 2},
	{Slug: "overhead_press", Name: "Overhead Press", Kind: "barbell", DefaultSets: 5, DefaultReps: 5, DefaultWeightKg: 40, SortOrder: 3},
	{Slug: "barbell_row", Name: "Barbell Row", Kind: "barbell", DefaultSets: 3, DefaultReps: 5, DefaultWeightKg: 70, SortOrder: 4},
	{Slug: "deadlift", Name: "Deadlift", Kind: "barbell", DefaultSets: 3, DefaultReps: 5, DefaultWeightKg: 70, SortOrder: 5},
	{Slug: "face_pull", Name: "Face Pull", Kind: "machine", DefaultSets: 5, DefaultReps: 5, DefaultWeightKg: 30, SortOrder: 6},
	{Slug: "lat_pulldown", Name: "Lat Pulldown", Kind: "machine", DefaultSets: 5, DefaultReps: 5, DefaultWeightKg: 65, SortOrder: 7},
	{Slug: "walking", Name: "Walking", Kind: "cardio", DefaultSets: 1, DefaultReps: 1, DefaultWeightKg: 0, SortOrder: 8},
	{Slug: "dumbbell_curls", Name: "Dumbbell Curls", Kind: "dumbbell", DefaultSets: 5, DefaultReps: 5, DefaultWeightKg: 15, SortOrder: 9},
	{Slug: "dips", Name: "Dips", Kind: "dumbbell", DefaultSets: 3, DefaultReps: 10, DefaultWeightKg: 0, SortOrder: 10},
	{Slug: "pullups", Name: "Pullups", Kind: "dumbbell", DefaultSets: 3, DefaultReps: 5, DefaultWeightKg: 0, SortOrder: 11},
}

func SeedExercises(ctx context.Context, q *Queries) error {
	for _, ex := range SeededExercises {
		if err := q.UpsertExercise(ctx, ex); err != nil {
			return err
		}
	}
	return nil
}
