package dnd

import (
	"testing"
	"time"
)

func TestWindow_Active_NonWrapping_InsideWindow(t *testing.T) {
	w := Window{Start: "00:00", End: "10:00"}
	got := w.Active(time.Date(2026, 7, 27, 5, 0, 0, 0, time.Local))
	if !got {
		t.Error("Active(05:00) = false, want true for a 00:00-10:00 window")
	}
}

func TestWindow_Active_NonWrapping(t *testing.T) {
	w := Window{Start: "00:00", End: "10:00"}
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"before start", time.Date(2026, 7, 27, 23, 59, 0, 0, time.Local), false},
		{"at start", time.Date(2026, 7, 27, 0, 0, 0, 0, time.Local), true},
		{"inside", time.Date(2026, 7, 27, 5, 0, 0, 0, time.Local), true},
		{"at end", time.Date(2026, 7, 27, 10, 0, 0, 0, time.Local), false},
		{"after end", time.Date(2026, 7, 27, 15, 0, 0, 0, time.Local), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.Active(tc.t); got != tc.want {
				t.Errorf("Active(%v) = %v, want %v", tc.t, got, tc.want)
			}
		})
	}
}

func TestWindow_Active_Wrapping(t *testing.T) {
	w := Window{Start: "22:00", End: "06:00"}
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"before start", time.Date(2026, 7, 27, 21, 0, 0, 0, time.Local), false},
		{"at start", time.Date(2026, 7, 27, 22, 0, 0, 0, time.Local), true},
		{"inside (late night)", time.Date(2026, 7, 27, 23, 30, 0, 0, time.Local), true},
		{"inside (early morning)", time.Date(2026, 7, 27, 3, 0, 0, 0, time.Local), true},
		{"at end", time.Date(2026, 7, 27, 6, 0, 0, 0, time.Local), false},
		{"after end", time.Date(2026, 7, 27, 12, 0, 0, 0, time.Local), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.Active(tc.t); got != tc.want {
				t.Errorf("Active(%v) = %v, want %v", tc.t, got, tc.want)
			}
		})
	}
}

func TestWindow_Active_MalformedTimes_FallBackToTenAM(t *testing.T) {
	// Both Start and End fall back to 10:00 on malformed input, producing a
	// zero-width window (never active) — exercises parseTime's fallback
	// without needing to export it.
	w := Window{Start: "garbage", End: "also-garbage"}
	if w.Active(time.Date(2026, 7, 27, 10, 0, 0, 0, time.Local)) {
		t.Error("Active(10:00) = true, want false: both malformed times fall back to the same 10:00, a zero-width window")
	}

	w2 := Window{Start: "garbage", End: "12:00"}
	if !w2.Active(time.Date(2026, 7, 27, 11, 0, 0, 0, time.Local)) {
		t.Error("Active(11:00) = false, want true: malformed Start falls back to 10:00, so 10:00-12:00 should be active at 11:00")
	}
}
