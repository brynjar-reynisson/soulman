package logmonitor

import "testing"

func TestDedupState_SeenBefore_UnseenPair_False(t *testing.T) {
	d := newDedupState()
	if d.seenBefore("memory-svc", "DB insert failed") {
		t.Error("seenBefore should be false for a never-marked pair")
	}
}
