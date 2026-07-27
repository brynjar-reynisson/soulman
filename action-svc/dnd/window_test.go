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
