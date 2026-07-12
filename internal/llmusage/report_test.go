package llmusage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDailyReport_Totals(t *testing.T) {
	r := DailyReport{
		ByTrigger: map[Trigger]Aggregate{
			TriggerBatch: {Requests: 2, TotalTokens: 100, EstimatedCostUSD: 0.01},
			TriggerSlack: {Requests: 3, TotalTokens: 200, EstimatedCostUSD: 0.02},
		},
	}
	if got := r.TotalRequests(); got != 5 {
		t.Errorf("TotalRequests() = %d, want 5", got)
	}
	if got := r.TotalTokens(); got != 300 {
		t.Errorf("TotalTokens() = %d, want 300", got)
	}
	if got := r.TotalCostUSD(); got != 0.03 {
		t.Errorf("TotalCostUSD() = %v, want 0.03", got)
	}
}

func TestFormatSlackMessage(t *testing.T) {
	tests := []struct {
		name       string
		report     DailyReport
		wantSubstr []string
		wantNot    []string
	}{
		{
			name: "しきい値未満は警告なし",
			report: DailyReport{
				Date: "2026-07-11",
				ByTrigger: map[Trigger]Aggregate{
					TriggerBatch: {Requests: 1, TotalTokens: 100, EstimatedCostUSD: 0.01},
				},
				AlertThresholdUSD: 10,
			},
			wantSubstr: []string{"2026-07-11", "推定コスト合計: $0.0100", "リクエスト数: 1件", "batch: 1件"},
			wantNot:    []string{":warning:"},
		},
		{
			name: "しきい値以上は警告あり",
			report: DailyReport{
				Date: "2026-07-11",
				ByTrigger: map[Trigger]Aggregate{
					TriggerBatch: {Requests: 1, TotalTokens: 100, EstimatedCostUSD: 15},
				},
				AlertThresholdUSD: 10,
			},
			wantSubstr: []string{":warning:", "しきい値 $10.00 を超過"},
		},
		{
			name: "しきい値0は警告機能を無効化",
			report: DailyReport{
				Date: "2026-07-11",
				ByTrigger: map[Trigger]Aggregate{
					TriggerBatch: {Requests: 1, TotalTokens: 100, EstimatedCostUSD: 999},
				},
				AlertThresholdUSD: 0,
			},
			wantNot: []string{":warning:"},
		},
		{
			name: "利用0件でもフォーマットできる",
			report: DailyReport{
				Date:      "2026-07-11",
				ByTrigger: map[Trigger]Aggregate{},
			},
			wantSubstr: []string{"推定コスト合計: $0.0000", "リクエスト数: 0件"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatSlackMessage(tt.report)
			for _, s := range tt.wantSubstr {
				if !strings.Contains(got, s) {
					t.Errorf("message missing %q\ngot:\n%s", s, got)
				}
			}
			for _, s := range tt.wantNot {
				if strings.Contains(got, s) {
					t.Errorf("message unexpectedly contains %q\ngot:\n%s", s, got)
				}
			}
		})
	}
}

// fakeStore is a canned llmusage.Store for BuildDailyReport tests.
type fakeStore struct {
	byTrigger    map[Trigger]Aggregate
	byTriggerErr error
	costSince    float64
	costSinceErr error
	gotFrom      []time.Time
	gotTo        []time.Time
}

func (f *fakeStore) DailyByTrigger(_ context.Context, from, to time.Time) (map[Trigger]Aggregate, error) {
	f.gotFrom = append(f.gotFrom, from)
	f.gotTo = append(f.gotTo, to)
	if f.byTriggerErr != nil {
		return nil, f.byTriggerErr
	}
	return f.byTrigger, nil
}

func (f *fakeStore) CostSince(_ context.Context, from, to time.Time) (float64, error) {
	f.gotFrom = append(f.gotFrom, from)
	f.gotTo = append(f.gotTo, to)
	if f.costSinceErr != nil {
		return 0, f.costSinceErr
	}
	return f.costSince, nil
}

func TestBuildDailyReport(t *testing.T) {
	day := time.Date(2026, 7, 11, 15, 30, 0, 0, time.UTC) // an arbitrary time within the target day
	st := &fakeStore{
		byTrigger: map[Trigger]Aggregate{TriggerBatch: {Requests: 1, TotalTokens: 10, EstimatedCostUSD: 0.001}},
		costSince: 1.23,
	}

	report, err := BuildDailyReport(context.Background(), st, day, 5)
	if err != nil {
		t.Fatalf("BuildDailyReport: %v", err)
	}
	if report.Date != "2026-07-11" {
		t.Errorf("Date = %q, want 2026-07-11", report.Date)
	}
	if report.MonthToDateCostUSD != 1.23 {
		t.Errorf("MonthToDateCostUSD = %v, want 1.23", report.MonthToDateCostUSD)
	}
	if report.AlertThresholdUSD != 5 {
		t.Errorf("AlertThresholdUSD = %v, want 5", report.AlertThresholdUSD)
	}
	if a := report.ByTrigger[TriggerBatch]; a.Requests != 1 {
		t.Errorf("ByTrigger[batch].Requests = %d, want 1", a.Requests)
	}

	wantFrom := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	if !st.gotFrom[0].Equal(wantFrom) || !st.gotTo[0].Equal(wantTo) {
		t.Errorf("DailyByTrigger range = [%v, %v), want [%v, %v)", st.gotFrom[0], st.gotTo[0], wantFrom, wantTo)
	}
	wantMonthStart := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if !st.gotFrom[1].Equal(wantMonthStart) || !st.gotTo[1].Equal(wantTo) {
		t.Errorf("CostSince range = [%v, %v), want [%v, %v)", st.gotFrom[1], st.gotTo[1], wantMonthStart, wantTo)
	}
}

func TestBuildDailyReport_PropagatesStoreErrors(t *testing.T) {
	tests := []struct {
		name string
		st   *fakeStore
	}{
		{name: "DailyByTriggerのエラーを伝播する", st: &fakeStore{byTriggerErr: errors.New("boom")}},
		{name: "CostSinceのエラーを伝播する", st: &fakeStore{costSinceErr: errors.New("boom")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildDailyReport(context.Background(), tt.st, time.Now(), 0); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}
