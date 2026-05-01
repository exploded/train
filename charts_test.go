package main

import (
	"database/sql"
	"testing"
	"time"

	"train/db"
)

func TestBuildChartFor_StatusAndOrder(t *testing.T) {
	// Need appLocation for niceShortDate; init if not set by main.
	if appLocation == nil {
		appLocation = time.UTC
	}

	ex := db.Exercise{ID: 1, Name: "Bench Press", Slug: "bench_press", DefaultSets: 5}
	rows := []db.ListWeightHistoryForExerciseRow{
		// Newest first (mirrors query ORDER BY DESC). 3 workouts:
		// w3 2026-05-01 60 kg fully successful (5/5)
		// w2 2026-04-29 57.5 kg partial (4/5)
		// w1 2026-04-27 57.5 kg untapped
		{WorkoutID: 3, WorkoutDate: "2026-05-01", WeightKg: 60, SetIndex: 1, TargetReps: 5, ActualReps: sql.NullInt64{Int64: 5, Valid: true}},
		{WorkoutID: 3, WorkoutDate: "2026-05-01", WeightKg: 60, SetIndex: 2, TargetReps: 5, ActualReps: sql.NullInt64{Int64: 5, Valid: true}},
		{WorkoutID: 2, WorkoutDate: "2026-04-29", WeightKg: 57.5, SetIndex: 1, TargetReps: 5, ActualReps: sql.NullInt64{Int64: 5, Valid: true}},
		{WorkoutID: 2, WorkoutDate: "2026-04-29", WeightKg: 57.5, SetIndex: 2, TargetReps: 5, ActualReps: sql.NullInt64{Int64: 4, Valid: true}},
		{WorkoutID: 1, WorkoutDate: "2026-04-27", WeightKg: 57.5, SetIndex: 1, TargetReps: 5},
		{WorkoutID: 1, WorkoutDate: "2026-04-27", WeightKg: 57.5, SetIndex: 2, TargetReps: 5},
	}

	vc := buildChartFor(ex, rows)
	if !vc.HasData {
		t.Fatal("expected HasData=true")
	}
	if len(vc.Points) != 3 {
		t.Fatalf("expected 3 points, got %d", len(vc.Points))
	}
	// Chronological order: oldest first
	wantStatus := []string{"untapped", "partial", "ok"}
	wantWeight := []float64{57.5, 57.5, 60}
	wantDate := []string{"2026-04-27", "2026-04-29", "2026-05-01"}
	for i, p := range vc.Points {
		if p.Status != wantStatus[i] {
			t.Errorf("point[%d].Status = %q, want %q", i, p.Status, wantStatus[i])
		}
		if p.WeightKg != wantWeight[i] {
			t.Errorf("point[%d].WeightKg = %v, want %v", i, p.WeightKg, wantWeight[i])
		}
		if p.Date != wantDate[i] {
			t.Errorf("point[%d].Date = %q, want %q", i, p.Date, wantDate[i])
		}
	}
	if vc.WeightDisp != "60" {
		t.Errorf("WeightDisp = %q, want \"60\"", vc.WeightDisp)
	}
}

func TestBuildChartFor_Empty(t *testing.T) {
	vc := buildChartFor(db.Exercise{Slug: "x"}, nil)
	if vc.HasData {
		t.Error("expected HasData=false for empty input")
	}
}

func TestPillStatus(t *testing.T) {
	cases := []struct {
		name string
		in   []db.Set
		want string
	}{
		{"all untapped",
			[]db.Set{{TargetReps: 5}, {TargetReps: 5}}, "untapped"},
		{"all hit",
			[]db.Set{{TargetReps: 5, ActualReps: sql.NullInt64{Int64: 5, Valid: true}}}, "ok"},
		{"one short",
			[]db.Set{
				{TargetReps: 5, ActualReps: sql.NullInt64{Int64: 5, Valid: true}},
				{TargetReps: 5, ActualReps: sql.NullInt64{Int64: 4, Valid: true}},
			}, "partial"},
		{"some tapped some not but all hits",
			[]db.Set{
				{TargetReps: 5, ActualReps: sql.NullInt64{Int64: 5, Valid: true}},
				{TargetReps: 5},
			}, "ok"},
	}
	for _, c := range cases {
		got := pillStatus(c.in)
		if got != c.want {
			t.Errorf("%s: pillStatus = %q, want %q", c.name, got, c.want)
		}
	}
}
