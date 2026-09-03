package llm

import (
	"context"
	"time"
)

// Classifier judges whether an email is important enough that the user
// should look at it as soon as possible, based on its sender, subject, and
// body. *DeepSeekClient is the production implementation (see
// deepseek.go); tests inject a fake to exercise
// thinking-svc/rules.GmailTriageRule without a network call or a real
// DEEPSEEK_API_KEY.
type Classifier interface {
	ClassifyImportance(ctx context.Context, sender, subject, body string) (important bool, reason string, err error)
}

// SchoolEvent is one actionable school date/time extracted from an email.
// Date is always an absolute "YYYY-MM-DD" — the classifier resolves any
// relative phrase ("this Friday") against the referenceDate it's given,
// not against real wall-clock time, so a backfilled email from weeks ago
// resolves correctly. See
// docs/superpowers/specs/2026-09-03-school-email-events-design.md.
type SchoolEvent struct {
	Date        string
	HasTime     bool
	Time        string // "HH:MM", only meaningful when HasTime
	Description string
}

// SchoolEventExtractor pulls zero or more actionable dates/times out of a
// school email. Mirrors Classifier's fail-closed convention: production
// *DeepSeekClient never returns a non-nil error — any failure collapses to
// (nil events, a "extraction unavailable: ..." note, nil error) so an LLM
// hiccup never blocks the report entry the caller always writes.
// relevantGrades, when non-empty, filters extracted events to those
// relevant to the given grades (e.g. []string{"5", "8"}) — a
// whole-school/ungraded announcement still counts, but an event specific
// to a different grade does not. Empty means no filtering.
type SchoolEventExtractor interface {
	ExtractSchoolEvents(ctx context.Context, sender, subject, body string, referenceDate time.Time, relevantGrades []string) (events []SchoolEvent, note string, err error)
}

// Client composes both LLM capabilities thinking-svc's rules currently
// need. Rule.Handle takes a single Client rather than one parameter per
// capability, so a future rule needing a new LLM capability grows this
// interface instead of Rule.Handle's parameter list.
type Client interface {
	Summarizer
	Classifier
	SchoolEventExtractor
}

// classifierSystemPrompt is deliberately a plain string constant — the
// single easiest place to tweak how importance is judged. v1 shipped with
// pure LLM judgment and no seeded criteria (see
// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md); this
// revision adds explicit criteria after observing real false positives —
// newsletter/digest content matching on scary keywords ("hack", "breach")
// about third-party incidents, and routine account-notification emails
// (sign-in confirmed, password changed) getting flagged important purely
// because they're topically about "security." The correction/feedback
// loop for further tuning is still out of scope for now — this is a
// manual hand-tune, not an automated one.
const classifierSystemPrompt = `You judge whether an email is important enough that the user should look at it as soon as possible. Judge from the recipient's own perspective — is this specifically about their account, their money, or something requiring their personal action, not a general news item.

Mark as NOT important: newsletters, digests, and marketing email, even if the subject mentions words like "hack," "breach," "urgent," or "security" — those usually describe third-party incidents being reported on, not anything happening to the recipient. Also mark as NOT important: routine account notifications (sign-in confirmations, password-changed confirmations, "new device added," data-sharing confirmations) that read as informational — these are typically the result of the recipient's own actions, especially when phrased like "if you didn't do this, secure your account" (a standard disclaimer, not a threat indicator).

Reserve important for: genuine deadline-driven or financial/legal matters, a real person needing a response, or a security notification that itself indicates unrecognized/suspicious activity (not a routine confirmation).

Respond with strict JSON only, no markdown and no extra text, in exactly this shape: {"important": true or false, "reason": "<one-sentence reason, under 140 characters>"}.`

// schoolEventExtractorSystemPromptBase is the always-present portion of
// the extraction prompt, same tuning posture as classifierSystemPrompt.
// %s is filled with the reference date ("2006-01-02") the caller
// supplies — see SchoolEvent's doc comment for why that's the email's own
// received date, not real "now". buildSchoolExtractorPrompt (deepseek.go)
// assembles the full prompt, optionally inserting
// schoolEventExtractorSystemPromptGradeClause before the JSON-shape
// instruction.
const schoolEventExtractorSystemPromptBase = `You extract actionable school dates/times from an email sent by a school or municipal school system. The reference date for resolving relative phrases ("this Friday," "next Tuesday," "tomorrow") is %s — resolve against that date, not any other notion of "today."

Only include events where a child or parent needs to DO something or BE somewhere on a specific date: a special clothing/theme day, a packed-lunch day, a field trip, a meeting, an event with a start time. Do not include general announcements with no specific date, or purely informational content.

The "description" field must be a short, verbatim excerpt from the email in its ORIGINAL language — quote the relevant sentence(s) directly, do not translate or paraphrase into English.

If the email is not actually about a school date (e.g. an unrelated municipal notice) or contains no such actionable date, return an empty array.`

// schoolEventExtractorSystemPromptGradeClause is appended only when the
// caller supplies a non-empty relevantGrades list. %s is filled with the
// grades joined by ", " (e.g. "5, 8").
const schoolEventExtractorSystemPromptGradeClause = `

Only include events relevant to these grades: %s. A whole-school or ungraded announcement still counts as relevant. An event specifically about a different grade or class does not.`

const schoolEventExtractorSystemPromptSuffix = `

Respond with strict JSON only, no markdown, exactly this shape: {"events": [{"date": "YYYY-MM-DD", "has_time": true or false, "time": "HH:MM" or "", "description": "<short phrase>"}]}`
