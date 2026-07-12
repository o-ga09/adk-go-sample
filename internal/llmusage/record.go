package llmusage

import (
	"context"
	"time"
)

// Entry is one recorded LLM call.
type Entry struct {
	Timestamp time.Time
	Model     string
	Trigger   Trigger

	// Populated from the ADK's agent.InvocationContext when available (every
	// trigger surface runs through the ADK runner, so this is always
	// present in practice).
	AppName      string
	UserID       string
	SessionID    string
	InvocationID string

	PromptTokens        int32
	CandidatesTokens    int32
	CachedTokens        int32
	ThoughtsTokens      int32
	ToolUsePromptTokens int32
	TotalTokens         int32

	EstimatedCostUSD float64
}

// Recorder persists a single Entry. Implementations must not return an
// error and must not let a persistence failure propagate: recording is a
// side channel, and per the source issue's acceptance criteria a recording
// failure must never break the agent turn it was measuring. Log failures
// internally instead (see store.UsageRecorder for the MySQL-backed
// implementation).
type Recorder interface {
	Record(ctx context.Context, e Entry)
}

// NoopRecorder discards every entry. Used when MYSQL_DSN is unset so local
// development works without a database.
type NoopRecorder struct{}

// Record implements Recorder by discarding e.
func (NoopRecorder) Record(context.Context, Entry) {}
