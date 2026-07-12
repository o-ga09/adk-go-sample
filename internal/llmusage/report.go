package llmusage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/o-ga09/adk-go-sample/internal/slackfmt"
	"github.com/slack-go/slack"
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

// Exceeded reports whether r's total cost has reached AlertThresholdUSD
// (always false when AlertThresholdUSD is 0, which disables the warning).
func (r DailyReport) Exceeded() bool {
	return r.AlertThresholdUSD > 0 && r.TotalCostUSD() >= r.AlertThresholdUSD
}

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

// FormatSlackMessage renders r as a Slack Block Kit daily cost report: a
// header, a headline warning when the threshold is exceeded, a fields block
// with the total cost/requests/tokens, a per-trigger breakdown, month-to-date
// cost, and a context footnote about the figures being estimates. Callers
// (PostToSlack) additionally wrap these blocks in a colored attachment when
// r.Exceeded() to make the overrun visually stand out.
func FormatSlackMessage(r DailyReport) []slack.Block {
	blocks := []slack.Block{
		slackfmt.Header(fmt.Sprintf(":moneybag: LLM利用コスト日次レポート (%s)", r.Date)),
	}

	if r.Exceeded() {
		blocks = append(blocks, slackfmt.Sections(fmt.Sprintf(":warning: *しきい値 $%.2f を超過しました*", r.AlertThresholdUSD))...)
	}

	blocks = append(blocks, slackfmt.Fields(
		"推定コスト合計", fmt.Sprintf("$%.4f", r.TotalCostUSD()),
		"リクエスト数", fmt.Sprintf("%d件", r.TotalRequests()),
		"トークン数合計", fmt.Sprintf("%d", r.TotalTokens()),
		"当月累計", fmt.Sprintf("$%.4f", r.MonthToDateCostUSD),
	))

	var breakdown strings.Builder
	for _, trig := range reportedTriggers {
		a, ok := r.ByTrigger[trig]
		if !ok {
			continue
		}
		fmt.Fprintf(&breakdown, "・%s: %d件 / %dトークン / $%.4f\n", trig, a.Requests, a.TotalTokens, a.EstimatedCostUSD)
	}
	if breakdown.Len() > 0 {
		blocks = append(blocks, slackfmt.Sections(strings.TrimRight(breakdown.String(), "\n"))...)
	}

	blocks = append(blocks, slackfmt.Context("※ 単価テーブルによる推定値です。実際の請求額とは異なる場合があります。"))

	return slackfmt.Limit(blocks)
}
