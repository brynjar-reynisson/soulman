package rules

import (
	"context"

	"soulman/common"
	"soulman/thinking-svc/llm"
)

// Rule matches a Stimulus and, on match, builds the common.ActionRequest to
// publish. Expressed as a value (not just a function) so future rules can
// carry a Name for logging/debugging.
type Rule struct {
	Name   string
	Match  func(*common.Stimulus) bool
	Handle func(ctx context.Context, s *common.Stimulus, client llm.Client) (*common.ActionRequest, error)
}

// Registry is the ordered list of rules evaluated against each Stimulus.
// Ordered (not a map) so future rules can be appended without restructuring;
// the first match wins.
var Registry = []Rule{
	ErrorReportRule,
	GmailTriageRule,
	CLINoteRule,
	SystemMonitorRule,
}

// Match returns a pointer to the first rule in Registry whose Match
// function returns true for s, or nil if no rule matches.
func Match(s *common.Stimulus) *Rule {
	for i := range Registry {
		if Registry[i].Match(s) {
			return &Registry[i]
		}
	}
	return nil
}

// Process matches s against Registry and, on match, runs the rule's Handle.
// Returns (nil, nil) when no rule matches — the stimulus is a no-op, ACKed
// and dropped by the caller.
func Process(ctx context.Context, s *common.Stimulus, client llm.Client) (*common.ActionRequest, error) {
	r := Match(s)
	if r == nil {
		return nil, nil
	}
	return r.Handle(ctx, s, client)
}
