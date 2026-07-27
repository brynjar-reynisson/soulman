package logmonitor

import "sync"

// dedupKey uniquely identifies one (service, message) pair for the life of
// this process. Built with a NUL separator (not simple concatenation) so a
// service name that happens to end where a message begins can never
// collide with a different split of the same concatenated string.
type dedupKey string

func newDedupKey(service, msg string) dedupKey {
	return dedupKey(service + "\x00" + msg)
}

// dedupState tracks which (service, msg) pairs have already fired a
// Stimulus this process lifetime. Mutex-guarded because Watcher's fsnotify
// event loop and its periodic reconciliation loop both call into it from
// separate goroutines. Not persisted across restarts — see the design
// spec's Dedup section for why that's an accepted tradeoff, the same one
// sysmonitor's in-memory severity state already makes.
type dedupState struct {
	mu   sync.Mutex
	seen map[dedupKey]struct{}
}

func newDedupState() *dedupState {
	return &dedupState{seen: map[dedupKey]struct{}{}}
}

// seenBefore reports whether (service, msg) has already fired this process
// lifetime, without marking it seen — callers check this before deciding
// whether to build and publish a Stimulus.
func (d *dedupState) seenBefore(service, msg string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.seen[newDedupKey(service, msg)]
	return ok
}

// markSeen records (service, msg) as fired. Callers must only call this
// after a successful publish — see Watcher.handleLine, which deliberately
// skips this call on a publish failure so the same line is retried on the
// next matching read rather than being permanently swallowed.
func (d *dedupState) markSeen(service, msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen[newDedupKey(service, msg)] = struct{}{}
}
