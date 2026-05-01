package main

import (
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"train/db"
)

// =============================================================================
// Chart geometry
// =============================================================================

// SVG layout for a single chart. iPhone-friendly: ~340px wide, 140px tall.
const (
	chartW         = 340
	chartH         = 140
	chartPadL      = 36 // room for Y axis labels
	chartPadR      = 12
	chartPadT      = 12
	chartPadB      = 22 // room for X axis label
	chartMaxPoints = 90 // limit how far back we look
)

type chartPoint struct {
	X        int     // SVG x
	Y        int     // SVG y
	WeightKg float64
	Date     string  // YYYY-MM-DD
	Status   string  // "ok" | "partial" | "untapped"
}

type viewChart struct {
	Name        string
	Slug        string
	Unit        string // "kg", "min", "kph", or "" (rendered after WeightDisp)
	Points      []chartPoint
	Polyline    string  // "x1,y1 x2,y2 ..."
	WeightDisp  string  // current value (last point)
	MinKgLabel  string
	MaxKgLabel  string
	YMaxY       int     // SVG y for max gridline label
	YMinY       int     // SVG y for min gridline label
	XFirstLabel string  // "1 Apr"
	XLastLabel  string  // "1 May"
	HasData     bool
}

type viewCharts struct {
	UserName  string
	ThemeMode string
	Charts    []viewChart
}

// buildChartFor turns history rows for one exercise into a viewChart with
// pre-computed SVG geometry. Each "row" is one set; we collapse to one point
// per workout (the workout's weight, plus an aggregate status for that day).
func buildChartFor(ex db.Exercise, rows []db.ListWeightHistoryForExerciseRow) viewChart {
	vc := viewChart{Name: ex.Name, Slug: ex.Slug, Unit: "kg"}
	if len(rows) == 0 {
		return vc
	}

	// Group rows by workout_id; pick weight from any set (they're all equal).
	type bucket struct {
		date     string
		weightKg float64
		setsHave int
		setsHit  int // actual_reps == target_reps
		anyTap   bool
	}
	byWk := map[int64]*bucket{}
	order := []int64{} // first-seen order
	for _, r := range rows {
		b, ok := byWk[r.WorkoutID]
		if !ok {
			b = &bucket{date: r.WorkoutDate, weightKg: r.WeightKg}
			byWk[r.WorkoutID] = b
			order = append(order, r.WorkoutID)
		}
		b.setsHave++
		if r.ActualReps.Valid {
			b.anyTap = true
			if r.ActualReps.Int64 == r.TargetReps {
				b.setsHit++
			}
		}
	}

	// Reverse to chronological (queries.sql returns DESC).
	chronoIDs := make([]int64, len(order))
	for i, id := range order {
		chronoIDs[len(order)-1-i] = id
	}
	// Sort by date for safety (same-day shouldn't happen but be defensive).
	sort.Slice(chronoIDs, func(i, j int) bool {
		return byWk[chronoIDs[i]].date < byWk[chronoIDs[j]].date
	})

	// Compute Y range with a tiny margin.
	minKg, maxKg := byWk[chronoIDs[0]].weightKg, byWk[chronoIDs[0]].weightKg
	for _, id := range chronoIDs {
		w := byWk[id].weightKg
		if w < minKg {
			minKg = w
		}
		if w > maxKg {
			maxKg = w
		}
	}
	if maxKg == minKg {
		// Pad by 5 kg either side for a flat line so it sits in the middle.
		minKg -= 5
		maxKg += 5
	}
	plotW := chartW - chartPadL - chartPadR
	plotH := chartH - chartPadT - chartPadB
	span := maxKg - minKg

	xFor := func(i int) int {
		if len(chronoIDs) == 1 {
			return chartPadL + plotW/2
		}
		return chartPadL + i*plotW/(len(chronoIDs)-1)
	}
	yFor := func(kg float64) int {
		// Higher weight = lower y in SVG.
		ratio := (kg - minKg) / span
		return chartPadT + plotH - int(ratio*float64(plotH))
	}

	// Build points + polyline.
	pts := make([]chartPoint, 0, len(chronoIDs))
	var polyBuf []byte
	for i, id := range chronoIDs {
		b := byWk[id]
		x, y := xFor(i), yFor(b.weightKg)
		status := "untapped"
		if b.anyTap {
			if b.setsHit == b.setsHave {
				status = "ok"
			} else {
				status = "partial"
			}
		}
		pts = append(pts, chartPoint{X: x, Y: y, WeightKg: b.weightKg, Date: b.date, Status: status})
		if i > 0 {
			polyBuf = append(polyBuf, ' ')
		}
		polyBuf = append(polyBuf, strconv.Itoa(x)...)
		polyBuf = append(polyBuf, ',')
		polyBuf = append(polyBuf, strconv.Itoa(y)...)
	}

	vc.Points = pts
	vc.Polyline = string(polyBuf)
	vc.WeightDisp = formatKg(byWk[chronoIDs[len(chronoIDs)-1]].weightKg)
	vc.MinKgLabel = formatKg(minKg)
	vc.MaxKgLabel = formatKg(maxKg)
	vc.YMinY = chartPadT + plotH
	vc.YMaxY = chartPadT
	vc.XFirstLabel = niceShortDate(byWk[chronoIDs[0]].date)
	vc.XLastLabel = niceShortDate(byWk[chronoIDs[len(chronoIDs)-1]].date)
	vc.HasData = true
	return vc
}

func niceShortDate(yyyymmdd string) string {
	// 2026-04-15 -> "15 Apr"
	t, err := time.ParseInLocation("2006-01-02", yyyymmdd, appLocation)
	if err != nil {
		return yyyymmdd
	}
	return t.Format("2 Jan")
}

// =============================================================================
// Generic series chart (used for walking)
// =============================================================================

// seriesPoint is one chronologically-ordered datum used by buildSeriesChart.
type seriesPoint struct {
	Date  string
	Value float64
}

// buildSeriesChart turns a chronological slice of seriesPoints into a
// viewChart with the same SVG layout as buildChartFor. fmtValue formats
// both axis labels and the current-value display.
func buildSeriesChart(name, slug, unit string, points []seriesPoint, fmtValue func(float64) string) viewChart {
	vc := viewChart{Name: name, Slug: slug, Unit: unit}
	if len(points) == 0 {
		return vc
	}
	minV, maxV := points[0].Value, points[0].Value
	for _, p := range points {
		if p.Value < minV {
			minV = p.Value
		}
		if p.Value > maxV {
			maxV = p.Value
		}
	}
	if maxV == minV {
		pad := maxV * 0.1
		if pad == 0 {
			pad = 1
		}
		minV -= pad
		maxV += pad
	}
	plotW := chartW - chartPadL - chartPadR
	plotH := chartH - chartPadT - chartPadB
	span := maxV - minV

	xFor := func(i int) int {
		if len(points) == 1 {
			return chartPadL + plotW/2
		}
		return chartPadL + i*plotW/(len(points)-1)
	}
	yFor := func(v float64) int {
		ratio := (v - minV) / span
		return chartPadT + plotH - int(ratio*float64(plotH))
	}

	pts := make([]chartPoint, 0, len(points))
	var polyBuf []byte
	for i, p := range points {
		x, y := xFor(i), yFor(p.Value)
		pts = append(pts, chartPoint{X: x, Y: y, WeightKg: p.Value, Date: p.Date, Status: "ok"})
		if i > 0 {
			polyBuf = append(polyBuf, ' ')
		}
		polyBuf = append(polyBuf, strconv.Itoa(x)...)
		polyBuf = append(polyBuf, ',')
		polyBuf = append(polyBuf, strconv.Itoa(y)...)
	}

	vc.Points = pts
	vc.Polyline = string(polyBuf)
	vc.WeightDisp = fmtValue(points[len(points)-1].Value)
	vc.MinKgLabel = fmtValue(minV)
	vc.MaxKgLabel = fmtValue(maxV)
	vc.YMinY = chartPadT + plotH
	vc.YMaxY = chartPadT
	vc.XFirstLabel = niceShortDate(points[0].Date)
	vc.XLastLabel = niceShortDate(points[len(points)-1].Date)
	vc.HasData = true
	return vc
}

// walkingSeriesFromHistory converts walking-history rows (date DESC) into
// three chronologically-ordered series: duration (min), speed (kph), incline.
// Only sessions marked done (actual_reps > 0) are charted.
func walkingSeriesFromHistory(rows []db.ListUserWalkingHistoryRow) (dur, speed, incline []seriesPoint) {
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if !r.ActualReps.Valid || r.ActualReps.Int64 <= 0 {
			continue
		}
		dur = append(dur, seriesPoint{Date: r.WorkoutDate, Value: float64(r.DurationMin)})
		speed = append(speed, seriesPoint{Date: r.WorkoutDate, Value: float64(r.SpeedX10) / 10})
		incline = append(incline, seriesPoint{Date: r.WorkoutDate, Value: float64(r.InclineX10) / 10})
	}
	return
}

func formatMinutes(v float64) string    { return strconv.Itoa(int(v)) }
func formatOneDecimal(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

// =============================================================================
// Handler
// =============================================================================

func handleCharts(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	exercises, err := queries.ListExercisesForUser(r.Context(), user.ID)
	if err != nil {
		serverError(w, "charts: list exercises", err)
		return
	}

	vc := viewCharts{
		UserName:  user.Name,
		ThemeMode: themeFromRequest(r),
	}
	for _, ex := range exercises {
		if ex.Kind == "cardio" {
			rows, err := queries.ListUserWalkingHistory(r.Context(), db.ListUserWalkingHistoryParams{
				UserID:     user.ID,
				ExerciseID: ex.ID,
				Limit:      int64(chartMaxPoints),
			})
			if err != nil {
				slog.Error("walking history", "error", err)
				continue
			}
			dur, speed, incline := walkingSeriesFromHistory(rows)
			vc.Charts = append(vc.Charts,
				buildSeriesChart(ex.Name+" - duration", ex.Slug+"_duration", "min", dur, formatMinutes),
				buildSeriesChart(ex.Name+" - speed", ex.Slug+"_speed", "kph", speed, formatOneDecimal),
				buildSeriesChart(ex.Name+" - incline", ex.Slug+"_incline", "", incline, formatOneDecimal),
			)
			continue
		}
		rows, err := queries.ListWeightHistoryForExercise(r.Context(), db.ListWeightHistoryForExerciseParams{
			UserID:     user.ID,
			ExerciseID: ex.ID,
			Limit:      int64(chartMaxPoints * int(ex.DefaultSets)), // sets per workout
		})
		if err != nil {
			slog.Error("history for exercise", "ex", ex.Slug, "error", err)
			continue
		}
		vc.Charts = append(vc.Charts, buildChartFor(ex, rows))
	}

	renderHTML(w, "charts.html", vc)
}
