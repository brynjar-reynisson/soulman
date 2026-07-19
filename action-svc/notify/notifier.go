package notify

// Notifier is implemented by anything capable of delivering a report as a
// single logical message. Selected via REPORT_NOTIFIER config; Discord is
// the only implementation in v1 — the interface exists so sms/email can be
// added later without touching the scheduler.
type Notifier interface {
	Send(message string) error
}
