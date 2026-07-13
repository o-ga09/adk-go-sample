package llmusage

import (
	"context"
	"iter"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
)

// WrapModel decorates m so that every GenerateContent response carrying
// usage metadata is priced against pricing and persisted via rec. This is
// called once in internal/app.Build, so every trigger surface that shares
// the built model (batch, Slack, the ADK REST API) is measured through the
// same code path.
func WrapModel(m model.LLM, rec Recorder, pricing PricingTable) model.LLM {
	return &recordingModel{inner: m, rec: rec, pricing: pricing}
}

type recordingModel struct {
	inner   model.LLM
	rec     Recorder
	pricing PricingTable
}

func (r *recordingModel) Name() string { return r.inner.Name() }

func (r *recordingModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		for resp, err := range r.inner.GenerateContent(ctx, req, stream) {
			// Partial is only meaningful in streaming mode: intermediate
			// chunks carry partial content, and only the final one has the
			// call's true usage totals. Non-streaming responses are never
			// partial, so this also runs for the batch/Slack triggers that
			// never stream.
			if err == nil && resp != nil && resp.UsageMetadata != nil && !resp.Partial {
				r.record(ctx, resp)
			}
			if !yield(resp, err) {
				return
			}
		}
	}
}

func (r *recordingModel) record(ctx context.Context, resp *model.LLMResponse) {
	u := resp.UsageMetadata
	modelName := r.inner.Name()

	e := Entry{
		Timestamp:           time.Now(),
		Model:               modelName,
		Trigger:             TriggerFromContext(ctx),
		PromptTokens:        u.PromptTokenCount,
		CandidatesTokens:    u.CandidatesTokenCount,
		CachedTokens:        u.CachedContentTokenCount,
		ThoughtsTokens:      u.ThoughtsTokenCount,
		ToolUsePromptTokens: u.ToolUsePromptTokenCount,
		TotalTokens:         u.TotalTokenCount,
		EstimatedCostUSD:    r.pricing.EstimateCostUSD(modelName, u.PromptTokenCount, u.CandidatesTokenCount, u.CachedContentTokenCount),
	}

	// The ctx the ADK passes into GenerateContent is an agent.InvocationContext
	// (it satisfies context.Context via embedding), which exposes the
	// session identifiers needed to attribute usage per-request.
	if ic, ok := ctx.(agent.InvocationContext); ok {
		e.InvocationID = ic.InvocationID()
		if s := ic.Session(); s != nil {
			e.AppName = s.AppName()
			e.UserID = s.UserID()
			e.SessionID = s.ID()
		}
	}

	r.rec.Record(ctx, e)
}
