package main

import "testing"

func TestPlatesForSide(t *testing.T) {
	cases := []struct {
		totalKg     float64
		wantWeights []float64
	}{
		{20.0, nil},                          // bar only
		{60.0, []float64{20}},                // 1x blue
		{70.0, []float64{20, 5}},             // 1x blue + 1x white
		{40.0, []float64{10}},                // 1x green per side
		{42.5, []float64{10, 1.25}},          // 1x green + 1x orange
		{50.0, []float64{10, 5}},             // 1x green + 1x white
		{62.5, []float64{20, 1.25}},          // 1x blue + 1x orange
		{65.0, []float64{20, 2.5}},           // 1x blue + 1x red
		{30.0, []float64{5}},                 // 1x white per side (under 10kg)
		{15.0, nil},                          // less than bar
	}
	for _, c := range cases {
		got := platesForSide(c.totalKg)
		if len(got) != len(c.wantWeights) {
			t.Errorf("totalKg=%v: got %d plates, want %d (%v)", c.totalKg, len(got), len(c.wantWeights), got)
			continue
		}
		for i, p := range got {
			if p.WeightKg != c.wantWeights[i] {
				t.Errorf("totalKg=%v: plate[%d].WeightKg = %v, want %v", c.totalKg, i, p.WeightKg, c.wantWeights[i])
			}
		}
	}
}

func TestFormatKg(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{60, "60"},
		{62.5, "62.5"},
		{1.25, "1.25"},
		{0, "0"},
	}
	for _, c := range cases {
		if got := formatKg(c.in); got != c.want {
			t.Errorf("formatKg(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
