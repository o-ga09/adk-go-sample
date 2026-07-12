package store

import (
	"context"
	"testing"
	"time"

	"github.com/o-ga09/adk-go-sample/internal/config"
	"github.com/o-ga09/adk-go-sample/internal/llmusage"
)

// TestNewUsageRecorder_NoopWhenMySQLDSNUnset replays the acceptance
// criterion "MYSQL_DSN未設定のローカル実行でもエラーにならない" for the
// usage recorder: no test database is available in CI (see the other
// internal/store tests), so this only exercises the no-op path, but that is
// the path every local `go run ./cmd/api` / `go run ./cmd/batch` actually
// takes without MYSQL_DSN set.
func TestNewUsageRecorder_NoopWhenMySQLDSNUnset(t *testing.T) {
	rec, err := NewUsageRecorder(&config.Config{})
	if err != nil {
		t.Fatalf("NewUsageRecorder: %v", err)
	}

	ctx := context.Background()

	// Record must not panic and must not require a live *gorm.DB.
	rec.Record(ctx, llmusage.Entry{Model: "gemini-2.5-flash", Trigger: llmusage.TriggerBatch})

	byTrigger, err := rec.DailyByTrigger(ctx, time.Now().Add(-24*time.Hour), time.Now())
	if err != nil {
		t.Errorf("DailyByTrigger: %v", err)
	}
	if byTrigger != nil {
		t.Errorf("DailyByTrigger = %v, want nil for a no-op recorder", byTrigger)
	}

	cost, err := rec.CostSince(ctx, time.Now().Add(-24*time.Hour), time.Now())
	if err != nil {
		t.Errorf("CostSince: %v", err)
	}
	if cost != 0 {
		t.Errorf("CostSince = %v, want 0 for a no-op recorder", cost)
	}
}
