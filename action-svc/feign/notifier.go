package feign

import "soulman/action-svc/notify"

// gatedNotifier wraps a real notify.Notifier so its Send is only actually
// called when the Gate is disabled.
type gatedNotifier struct {
	gate *Gate
	real notify.Notifier
}

// WrapNotifier returns a notify.Notifier that delegates to real when gate
// is disabled, and records (instead of sending) when enabled. The wrapped
// Notifier is indistinguishable from a real one at the call site —
// notifybatch.Batcher and scheduler.Scheduler need no code changes to
// benefit from this.
func WrapNotifier(gate *Gate, real notify.Notifier) notify.Notifier {
	return &gatedNotifier{gate: gate, real: real}
}

func (n *gatedNotifier) Send(message string) error {
	if n.gate.Enabled() {
		return n.gate.Record("notify", message)
	}
	return n.real.Send(message)
}
