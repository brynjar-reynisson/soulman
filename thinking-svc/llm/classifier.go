package llm

import "context"

// Classifier judges whether an email is important enough that the user
// should look at it as soon as possible, based on its sender, subject, and
// body. *DeepSeekClient is the production implementation (see
// deepseek.go); tests inject a fake to exercise
// thinking-svc/rules.GmailTriageRule without a network call or a real
// DEEPSEEK_API_KEY.
type Classifier interface {
	ClassifyImportance(ctx context.Context, sender, subject, body string) (important bool, reason string, err error)
}

// Client composes both LLM capabilities thinking-svc's rules currently
// need. Rule.Handle takes a single Client rather than one parameter per
// capability, so a future rule needing a new LLM capability grows this
// interface instead of Rule.Handle's parameter list.
type Client interface {
	Summarizer
	Classifier
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
