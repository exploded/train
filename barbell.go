package main

// Plate denominations and visual config for the barbell render. The bar
// itself is 20 kg; per-side weight = (totalKg - 20) / 2.
//
// Color and height-percent are read by templates/partials/barbell.html.

const barWeightKg = 20.0

type plateDef struct {
	WeightKg float64
	Color    string // CSS color
	Stroke   string // border color (white plates need a darker outline)
	HeightPc int    // % of bar height
}

var plateDefs = []plateDef{
	{20.0, "#1d4ed8", "#1e3a8a", 100}, // blue
	{10.0, "#16a34a", "#14532d", 85},  // green
	{5.0, "#f8fafc", "#94a3b8", 70},   // white
	{2.5, "#dc2626", "#7f1d1d", 50},   // red
	{1.25, "#f97316", "#9a3412", 35},  // orange
}

type Plate struct {
	WeightKg float64
	Color    string
	Stroke   string
	X        int // SVG x position (left edge of plate rect)
	Y        int // SVG y position (top edge)
	Height   int // SVG height
	Width    int // SVG width
}

// SVG layout constants. The viewBox is 200x64; the bar sits centered
// vertically at y=29..35 (height=6).
const (
	svgHeight    = 64
	collarRightX = 14 // x where plates start (right of collar stub)
	plateWidth   = 10
	plateGap     = 2
)

// platesForSide returns the plate stack for one end of the bar (innermost
// first), with SVG positions precomputed. Greedy fill, descending
// denominations.
func platesForSide(totalKg float64) []Plate {
	per := (totalKg - barWeightKg) / 2.0
	if per <= 0 {
		return nil
	}
	const epsilon = 0.001
	x := collarRightX
	var out []Plate
	for _, p := range plateDefs {
		for per+epsilon >= p.WeightKg {
			h := svgHeight * p.HeightPc / 100
			y := (svgHeight - h) / 2
			out = append(out, Plate{
				WeightKg: p.WeightKg,
				Color:    p.Color,
				Stroke:   p.Stroke,
				X:        x,
				Y:        y,
				Height:   h,
				Width:    plateWidth,
			})
			x += plateWidth + plateGap
			per -= p.WeightKg
		}
	}
	return out
}
