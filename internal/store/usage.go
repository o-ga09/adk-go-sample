package store

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/o-ga09/adk-go-sample/internal/config"
	"github.com/o-ga09/adk-go-sample/internal/llmusage"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// UsageRecorder implements both llmusage.Recorder (write side, called from
// the model wrapped in internal/app.Build) and llmusage.Store (read side,
// used by cmd/batch's daily cost-report command). A zero-value UsageRecorder
// (db == nil) is a safe no-op, used when MYSQL_DSN is unset so local
// development and the acceptance criterion "MYSQL_DSN未設定のローカル実行で
// もエラーにならない" both hold without a separate no-op type.
type UsageRecorder struct {
	db *gorm.DB
}

// NewUsageRecorder opens a MySQL-backed UsageRecorder, or returns a no-op
// one when c.MySQLDSN is unset.
//
// Unlike NewSessionService, a connection error here is not returned to the
// caller as fatal: internal/app.Build treats it as "recording disabled" and
// falls back to a no-op UsageRecorder, per the source issue's requirement
// that a failure to record usage must never stop the agent itself from
// running. cmd/batch's cost-report command still surfaces the error, since
// there recording is the entire point of the run.
func NewUsageRecorder(c *config.Config) (*UsageRecorder, error) {
	if c.MySQLDSN == "" {
		return &UsageRecorder{}, nil
	}
	dsn, err := microsecondDSN(c.MySQLDSN)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	return &UsageRecorder{db: db}, nil
}

// Record implements llmusage.Recorder. It logs and swallows any error
// instead of propagating it, so a database hiccup never fails the agent
// turn being measured.
func (r *UsageRecorder) Record(ctx context.Context, e llmusage.Entry) {
	if r.db == nil {
		return
	}
	row := llmUsageSchema{
		Timestamp:           e.Timestamp,
		Model:               e.Model,
		Trigger:             string(e.Trigger),
		AppName:             e.AppName,
		UserID:              e.UserID,
		SessionID:           e.SessionID,
		InvocationID:        e.InvocationID,
		PromptTokens:        e.PromptTokens,
		CandidatesTokens:    e.CandidatesTokens,
		CachedTokens:        e.CachedTokens,
		ThoughtsTokens:      e.ThoughtsTokens,
		ToolUsePromptTokens: e.ToolUsePromptTokens,
		TotalTokens:         e.TotalTokens,
		EstimatedCostUSD:    e.EstimatedCostUSD,
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		log.Printf("llmusage: failed to record usage: %v", err)
	}
}

// DailyByTrigger implements llmusage.Store.
func (r *UsageRecorder) DailyByTrigger(ctx context.Context, from, to time.Time) (map[llmusage.Trigger]llmusage.Aggregate, error) {
	if r.db == nil {
		return nil, nil
	}
	var rows []struct {
		Trigger     string
		Requests    int
		TotalTokens int64
		CostUSD     float64
	}
	err := r.db.WithContext(ctx).Model(&llmUsageSchema{}).
		Select("trigger, COUNT(*) AS requests, COALESCE(SUM(total_tokens),0) AS total_tokens, COALESCE(SUM(estimated_cost_usd),0) AS cost_usd").
		Where("timestamp >= ? AND timestamp < ?", from, to).
		Group("trigger").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("aggregate llm_usages by trigger: %w", err)
	}
	out := make(map[llmusage.Trigger]llmusage.Aggregate, len(rows))
	for _, row := range rows {
		out[llmusage.Trigger(row.Trigger)] = llmusage.Aggregate{
			Requests:         row.Requests,
			TotalTokens:      row.TotalTokens,
			EstimatedCostUSD: row.CostUSD,
		}
	}
	return out, nil
}

// CostSince implements llmusage.Store.
func (r *UsageRecorder) CostSince(ctx context.Context, from, to time.Time) (float64, error) {
	if r.db == nil {
		return 0, nil
	}
	var total float64
	err := r.db.WithContext(ctx).Model(&llmUsageSchema{}).
		Where("timestamp >= ? AND timestamp < ?", from, to).
		Select("COALESCE(SUM(estimated_cost_usd),0)").
		Scan(&total).Error
	if err != nil {
		return 0, fmt.Errorf("sum llm_usages cost: %w", err)
	}
	return total, nil
}
