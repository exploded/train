package main

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"train/db"
)

const (
	successStreakBumpAt = 5    // bump weight after this many consecutive fully-successful sessions
	weightIncrementKg   = 2.5  // amount to bump (= 1.25 kg per side)
	weightDecrementKg   = -2.5 // explicit user-requested step in either direction
)

// runProgressionForUser is called once at the start of a new workout day,
// before any sets are created. For each exercise, it inspects the user's
// most recent prior workout: if every set for that exercise was successful,
// the streak ticks up; if the streak hits the bump threshold, weight goes up
// by 2.5 kg and the streak resets. Walking and dumbbell curls don't auto-
// progress.
func runProgressionForUser(ctx context.Context, q *db.Queries, userID int64, todayDate string) error {
	prev, err := q.GetLastWorkoutBefore(ctx, db.GetLastWorkoutBeforeParams{
		UserID: userID, WorkoutDate: todayDate,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil // first workout ever; nothing to evaluate
	}
	if err != nil {
		return err
	}

	exercises, err := q.ListExercises(ctx)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)

	for _, ex := range exercises {
		if !exerciseAutoProgresses(ex) {
			continue
		}

		sets, err := q.ListSetsForWorkoutExercise(ctx, db.ListSetsForWorkoutExerciseParams{
			WorkoutID: prev.ID, ExerciseID: ex.ID,
		})
		if err != nil {
			return err
		}
		if len(sets) == 0 {
			continue // exercise wasn't part of the previous workout
		}

		fullySuccessful := allSetsSuccessful(sets)

		uew, err := q.GetUserExerciseWeight(ctx, db.GetUserExerciseWeightParams{
			UserID: userID, ExerciseID: ex.ID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			// Lazy create at default weight if the user hasn't been seeded yet.
			uew = db.UserExerciseWeight{
				UserID: userID, ExerciseID: ex.ID,
				WeightKg: ex.DefaultWeightKg, SuccessStreak: 0,
			}
		} else if err != nil {
			return err
		}

		if fullySuccessful {
			uew.SuccessStreak++
			if uew.SuccessStreak >= successStreakBumpAt {
				uew.WeightKg += weightIncrementKg
				uew.SuccessStreak = 0
			}
		} else {
			uew.SuccessStreak = 0
		}

		if err := q.UpsertUserExerciseWeight(ctx, db.UpsertUserExerciseWeightParams{
			UserID:        userID,
			ExerciseID:    ex.ID,
			WeightKg:      uew.WeightKg,
			SuccessStreak: uew.SuccessStreak,
			UpdatedAt:     now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func exerciseAutoProgresses(ex db.Exercise) bool {
	if ex.Kind == "cardio" {
		return false
	}
	if ex.Slug == "dumbbell_curls" {
		return false // 1.25 kg dumbbell jumps are awkward; manual only
	}
	return true
}

func allSetsSuccessful(sets []db.Set) bool {
	if len(sets) == 0 {
		return false
	}
	for _, s := range sets {
		if !s.ActualReps.Valid || s.ActualReps.Int64 != s.TargetReps {
			return false
		}
	}
	return true
}
