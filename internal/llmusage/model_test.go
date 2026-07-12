package llmusage

import (
	"context"
	"iter"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// fakeLLM yields a fixed sequence of responses, ignoring the request.
type fakeLLM struct {
	name      string
	responses []*model.LLMResponse
}

func (f *fakeLLM) Name() string { return f.name }

func (f *fakeLLM) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		for _, r := range f.responses {
			if !yield(r, nil) {
				return
			}
		}
	}
}

// recordingSpy captures every Entry passed to Record.
type recordingSpy struct {
	entries []Entry
}

func (s *recordingSpy) Record(_ context.Context, e Entry) {
	s.entries = append(s.entries, e)
}

func TestWrapModel_RecordsOnlyResponsesWithFinalUsageMetadata(t *testing.T) {
	pricing := PricingTable{"fake-model": {InputPerMillionUSD: 1_000_000, OutputPerMillionUSD: 1_000_000}}

	tests := []struct {
		name      string
		responses []*model.LLMResponse
		wantCount int
	}{
		{
			name: "非ストリーミングの単一レスポンスは1件記録される",
			responses: []*model.LLMResponse{
				{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 5}},
			},
			wantCount: 1,
		},
		{
			name: "UsageMetadataがnilのレスポンスは記録されない",
			responses: []*model.LLMResponse{
				{Content: genai.NewContentFromText("chunk", genai.RoleModel)},
			},
			wantCount: 0,
		},
		{
			name: "Partial=trueの中間チャンクは記録されず最終チャンクのみ記録される",
			responses: []*model.LLMResponse{
				{Partial: true, UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 1}},
				{Partial: true, UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 2}},
				{Partial: false, UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 3}},
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &fakeLLM{name: "fake-model", responses: tt.responses}
			spy := &recordingSpy{}
			wrapped := WrapModel(inner, spy, pricing)

			for range wrapped.GenerateContent(context.Background(), &model.LLMRequest{}, false) {
			}

			if len(spy.entries) != tt.wantCount {
				t.Fatalf("recorded %d entries, want %d", len(spy.entries), tt.wantCount)
			}
		})
	}
}

func TestWrapModel_EntryFieldsAndCost(t *testing.T) {
	pricing := PricingTable{"fake-model": {InputPerMillionUSD: 1_000_000, OutputPerMillionUSD: 2_000_000}}
	inner := &fakeLLM{
		name: "fake-model",
		responses: []*model.LLMResponse{
			{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     10,
				CandidatesTokenCount: 5,
				TotalTokenCount:      15,
			}},
		},
	}
	spy := &recordingSpy{}
	wrapped := WrapModel(inner, spy, pricing)

	ctx := WithTrigger(context.Background(), TriggerBatch)
	for range wrapped.GenerateContent(ctx, &model.LLMRequest{}, false) {
	}

	if len(spy.entries) != 1 {
		t.Fatalf("recorded %d entries, want 1", len(spy.entries))
	}
	got := spy.entries[0]
	if got.Model != "fake-model" {
		t.Errorf("Model = %q, want fake-model", got.Model)
	}
	if got.Trigger != TriggerBatch {
		t.Errorf("Trigger = %q, want %q", got.Trigger, TriggerBatch)
	}
	if got.PromptTokens != 10 || got.CandidatesTokens != 5 || got.TotalTokens != 15 {
		t.Errorf("token counts = %+v, want prompt=10 candidates=5 total=15", got)
	}
	wantCost := 10.0 + 10.0 // 10 prompt tokens @ $1/token + 5 candidate tokens @ $2/token
	if got.EstimatedCostUSD != wantCost {
		t.Errorf("EstimatedCostUSD = %v, want %v", got.EstimatedCostUSD, wantCost)
	}
}

func TestWrapModel_ForwardsResponsesAndErrorsUnchanged(t *testing.T) {
	wantResp := &model.LLMResponse{Content: genai.NewContentFromText("hello", genai.RoleModel)}
	inner := &fakeLLM{name: "fake-model", responses: []*model.LLMResponse{wantResp}}
	wrapped := WrapModel(inner, &recordingSpy{}, PricingTable{})

	var got []*model.LLMResponse
	for resp, err := range wrapped.GenerateContent(context.Background(), &model.LLMRequest{}, false) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got = append(got, resp)
	}
	if len(got) != 1 || got[0] != wantResp {
		t.Errorf("GenerateContent did not forward the inner response unchanged")
	}
}

func TestWrapModel_Name(t *testing.T) {
	inner := &fakeLLM{name: "fake-model"}
	wrapped := WrapModel(inner, &recordingSpy{}, PricingTable{})
	if got := wrapped.Name(); got != "fake-model" {
		t.Errorf("Name() = %q, want fake-model", got)
	}
}
