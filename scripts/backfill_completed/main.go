// One-shot backfill: marks past workouts as completed when they have at least
// one logged (tapped) set but no completed_at. This fixes history that was
// loaded directly (e.g. a CSV import) without going through the in-app "Finish"
// flow, so it never received a completion timestamp. Such workouts otherwise
// render as "started" (orange) on the activity grid and are excluded from the
// home stats, even though they are real, finished sessions.
//
// The activity grid already treats a past day with logged sets as "done" at
// render time; this backfill makes the underlying data consistent so the home
// stats (this week / this month / streak) count those sessions too.
//
// Usage:
//
//	go run ./scripts/backfill_completed -train train.db
//	go run ./scripts/backfill_completed -dry-run
package main

import (
	"database/sql"
	"flag"
	"log"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	var (
		trainPath = flag.String("train", "train.db", "path to Train SQLite DB")
		tzName    = flag.String("tz", "Australia/Sydney", "IANA timezone for 'today' so today's in-progress workout is left alone")
		dryRun    = flag.Bool("dry-run", false, "report what would change without writing")
	)
	flag.Parse()

	loc, err := time.LoadLocation(*tzName)
	if err != nil {
		log.Fatalf("invalid -tz: %v", err)
	}
	if _, err := os.Stat(*trainPath); err != nil {
		log.Fatalf("cannot stat -train %q: %v", *trainPath, err)
	}

	trainDB, err := sql.Open("sqlite", *trainPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)")
	if err != nil {
		log.Fatalf("open Train: %v", err)
	}
	defer trainDB.Close()

	today := time.Now().In(loc).Format("2006-01-02")

	// Candidate workouts: in the past, no completion timestamp, but with at
	// least one tapped set. completed_at is set to noon UTC on the workout date
	// (only its presence matters to the app; the value stays inside the day).
	const sel = `
SELECT id, workout_date
FROM workouts
WHERE completed_at IS NULL
  AND workout_date < ?
  AND id IN (SELECT DISTINCT workout_id FROM sets WHERE actual_reps IS NOT NULL)
ORDER BY workout_date`

	rows, err := trainDB.Query(sel, today)
	if err != nil {
		log.Fatalf("select candidates: %v", err)
	}
	type cand struct {
		id   int64
		date string
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.date); err != nil {
			log.Fatalf("scan: %v", err)
		}
		cands = append(cands, c)
	}
	rows.Close()

	log.Printf("found %d past workout(s) with logged sets and no completed_at", len(cands))
	if *dryRun {
		for _, c := range cands {
			log.Printf("  would set completed_at on %s (id=%d)", c.date, c.id)
		}
		log.Printf("dry run: no changes written")
		return
	}

	tx, err := trainDB.Begin()
	if err != nil {
		log.Fatalf("begin: %v", err)
	}
	const upd = `UPDATE workouts SET completed_at = ? WHERE id = ?`
	for _, c := range cands {
		ts := c.date + "T12:00:00Z"
		if _, err := tx.Exec(upd, ts, c.id); err != nil {
			tx.Rollback()
			log.Fatalf("update %s (id=%d): %v", c.date, c.id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Fatalf("commit: %v", err)
	}
	log.Printf("backfilled completed_at on %d workout(s)", len(cands))
}
