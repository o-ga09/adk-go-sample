package llmusage

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Aggregate is a summed usage total for one grouping (e.g. one Trigger, over
// one day).
type Aggregate struct {
	Requests         int
	TotalTokens      int64
	EstimatedCostUSD float64
}

// Store is the read side the daily cost-report command needs.
// store.UsageRecorder implements it.
type Store interface {
	// DailyByTrigger sums usage recorded in [from, to) grouped by Trigger.
	DailyByTrigger(ctx context.Context, from, to time.Time) (map[Trigger]Aggregate, error)
	// CostSince sums EstimatedCostUSD recorded in [from, to).
	CostSince(ctx context.Context, from, to time.Time) (float64, error)
}

// DailyReport is one day's aggregated usage, ready to format for Slack.
type DailyReport struct {
	Date               string // JST YYYY-MM-DD
	ByTrigger          map[Trigger]Aggregate
	MonthToDateCostUSD float64
	AlertThresholdUSD  float64 // 0 disables the threshold warning
}

// reportedTriggers fixes the display order of FormatSlackMessage's
// per-trigger breakdown.
var reportedTriggers = []Trigger{TriggerBatch, TriggerSlack, TriggerAPI}

// TotalCostUSD sums EstimatedCostUSD across all triggers in the report.
func (r DailyReport) TotalCostUSD() float64 {
	var total float64
	for _, a := range r.ByTrigger {
		total += a.EstimatedCostUSD
	}
	return total
}

// TotalRequests sums Requests across all triggers in the report.
func (r DailyReport) TotalRequests() int {
	var total int
	for _, a := range r.ByTrigger {
		total += a.Requests
	}
	return total
}

// TotalTokens sums TotalTokens across all triggers in the report.
func (r DailyReport) TotalTokens() int64 {
	var total int64
	for _, a := range r.ByTrigger {
		total += a.TotalTokens
	}
	return total
}

// BuildDailyReport aggregates usage for the calendar day containing day
// (using day's own location, so callers control the timezone by passing a
// time already in it) plus the month-to-date cost through the end of that
// day, ready to format and post to Slack.
func BuildDailyReport(ctx context.Context, st Store, day time.Time, alertThresholdUSD float64) (DailyReport, error) {
	loc := day.Location()
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	to := from.AddDate(0, 0, 1)

	byTrigger, err := st.DailyByTrigger(ctx, from, to)
	if err != nil {
		return DailyReport{}, fmt.Errorf("aggregate daily usage: %w", err)
	}

	monthStart := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, loc)
	mtd, err := st.CostSince(ctx, monthStart, to)
	if err != nil {
		return DailyReport{}, fmt.Errorf("aggregate month-to-date cost: %w", err)
	}

	return DailyReport{
		Date:               from.Format("2006-01-02"),
		ByTrigger:          byTrigger,
		MonthToDateCostUSD: mtd,
		AlertThresholdUSD:  alertThresholdUSD,
	}, nil
}

// FormatSlackMessage renders r as the Japanese Slack mrkdwn daily cost
// summary.
func FormatSlackMessage(r DailyReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, ":moneybag: LLM利用コスト日次レポート (%s)\n", r.Date)

	if r.AlertThresholdUSD > 0 && r.TotalCostUSD() >= r.AlertThresholdUSD {
		fmt.Fprintf(&b, ":warning: しきい値 $%.2f を超過しました\n", r.AlertThresholdUSD)
	}

	fmt.Fprintf(&b, "・推定コスト合計: $%.4f\n", r.TotalCostUSD())
	fmt.Fprintf(&b, "・リクエスト数: %d件\n", r.TotalRequests())
	fmt.Fprintf(&b, "・トークン数合計: %d\n", r.TotalTokens())

	for _, trig := range reportedTriggers {
		a, ok := r.ByTrigger[trig]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "　- %s: %d件 / %dトークン / $%.4f\n", trig, a.Requests, a.TotalTokens, a.EstimatedCostUSD)
	}

	fmt.Fprintf(&b, "・当月累計: $%.4f\n", r.MonthToDateCostUSD)
	b.WriteString("\n※ 単価テーブルによる推定値です。実際の請求額とは異なる場合があります。")

	return strings.TrimRight(b.String(), "\n")
}
