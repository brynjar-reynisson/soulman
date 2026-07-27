package logmonitor

import "testing"

func TestDedupState_SeenBefore_UnseenPair_False(t *testing.T) {
	d := newDedupState()
	if d.seenBefore("memory-svc", "DB insert failed") {
		t.Error("seenBefore should be false for a never-marked pair")
	}
}

func TestDedupState_MarkSeen_ThenSeenBefore_True(t *testing.T) {
	d := newDedupState()
	d.markSeen("memory-svc", "DB insert failed")
	if !d.seenBefore("memory-svc", "DB insert failed") {
		t.Error("seenBefore should be true after markSeen")
	}
}

func TestDedupState_DifferentService_SameMsg_TrackedIndependently(t *testing.T) {
	d := newDedupState()
	d.markSeen("memory-svc", "DB insert failed")
	if d.seenBefore("action-svc", "DB insert failed") {
		t.Error("seenBefore should be false for the same msg from a different service")
	}
}

func TestDedupState_SameService_DifferentMsg_TrackedIndependently(t *testing.T) {
	d := newDedupState()
	d.markSeen("memory-svc", "DB insert failed")
	if d.seenBefore("memory-svc", "nats consumer start failed") {
		t.Error("seenBefore should be false for a different msg from the same service")
	}
}

func TestDedupState_NoServiceMsgBoundaryCollision(t *testing.T) {
	d := newDedupState()
	d.markSeen("ab", "cd")
	if d.seenBefore("a", "bcd") {
		t.Error("seenBefore should not collide across a different service/msg split of the same concatenated string")
	}
}
