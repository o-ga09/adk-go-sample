// Package llmusage measures Gemini token usage for every LLM call the agent
// makes, prices it against a per-model $/1M-token table, and persists it so a
// daily batch can report estimated cost to Slack. See internal/app.Build for
// where the model gets wrapped, and cmd/batch's "llm-cost-report" command for
// the reporting side.
package llmusage

import "context"

// Trigger identifies which surface caused an LLM call, so the daily report
// can break cost down by trigger.
type Trigger string

const (
	// TriggerBatch is the cron batch (cmd/batch, mail-triage command).
	TriggerBatch Trigger = "batch"
	// TriggerSlack is a Slack @mention handled by internal/slackbot.
	TriggerSlack Trigger = "slack"
	// TriggerAPI is the default: the plain ADK REST API, or any caller that
	// didn't tag its context with WithTrigger.
	TriggerAPI Trigger = "api"
)

type triggerKey struct{}

// WithTrigger attaches t to ctx so that any LLM call made while running the
// ADK agent under this context is recorded with it. cmd/batch and
// internal/slackbot call this before invoking the ADK runner; the plain ADK
// REST API path leaves ctx untagged and is recorded as TriggerAPI.
func WithTrigger(ctx context.Context, t Trigger) context.Context {
	return context.WithValue(ctx, triggerKey{}, t)
}

// TriggerFromContext reads the trigger tagged onto ctx by WithTrigger,
// defaulting to TriggerAPI when none was set.
func TriggerFromContext(ctx context.Context) Trigger {
	if t, ok := ctx.Value(triggerKey{}).(Trigger); ok {
		return t
	}
	return TriggerAPI
}
