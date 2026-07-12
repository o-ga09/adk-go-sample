package weeklyreview

import (
	"strings"
	"testing"
	"time"

	"github.com/o-ga09/adk-go-sample/internal/store"
)

func TestFormatSlackMessage(t *testing.T) {
	due := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		report     Report
		wantSubstr []string
		wantNot    []string
	}{
		{
			name: "滞留タスクなしなら滞留セクションを省略",
			report: Report{
				Date:       "2026-07-12",
				StaleDays:  14,
				InboxCount: 3,
				NextUp:     []store.Task{{ID: "t1", Title: "企画書レビュー", Due: &due}},
			},
			wantSubstr: []string{"2026-07-12", "未整理(inbox): 3件", "企画書レビュー"},
			wantNot:    []string{"停滞"},
		},
		{
			name: "滞留タスクがあれば一覧表示する",
			report: Report{
				Date:       "2026-07-12",
				StaleDays:  14,
				InboxCount: 0,
				Stalled:    []store.Task{{ID: "t2", Title: "見積もり確認", Status: store.TaskStatusWaiting}},
			},
			wantSubstr: []string{"停滞", "14日", "見積もり確認"},
		},
		{
			name: "次にやることが無ければその旨を出す",
			report: Report{
				Date:       "2026-07-12",
				InboxCount: 0,
			},
			wantSubstr: []string{"未整理(inbox): 0件"},
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
