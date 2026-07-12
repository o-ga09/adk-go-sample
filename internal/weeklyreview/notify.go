package weeklyreview

import (
	"context"
	"fmt"
	"strings"

	"github.com/o-ga09/adk-go-sample/internal/store"
	"github.com/slack-go/slack"
)

// FormatSlackMessage renders r as the Japanese Slack mrkdwn weekly-review
// summary.
func FormatSlackMessage(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, ":clipboard: GTD週次レビュー (%s)\n", r.Date)

	fmt.Fprintf(&b, "・未整理(inbox): %d件\n", r.InboxCount)

	if len(r.Stalled) > 0 {
		fmt.Fprintf(&b, "・停滞タスク(%d日以上更新なし): %d件\n", r.StaleDays, len(r.Stalled))
		for _, t := range r.Stalled {
			fmt.Fprintf(&b, "　- [%s] %s\n", t.Status, t.Title)
		}
	}

	fmt.Fprintf(&b, "・今週やるべきこと: %d件\n", len(r.NextUp))
	for _, t := range r.NextUp {
		fmt.Fprintf(&b, "　- %s%s\n", t.Title, dueSuffix(t))
	}

	return strings.TrimRight(b.String(), "\n")
}

// dueSuffix renders " (期限: 2026-07-14)" for a task with a due date, or ""
// for one without.
func dueSuffix(t store.Task) string {
	if t.Due == nil {
		return ""
	}
	return fmt.Sprintf(" (期限: %s)", t.Due.Format("2006-01-02"))
}

// PostToSlack posts text to channel using a Slack Bot Token client. Like
// internal/llmusage's daily cost report, the weekly review is posted
// directly by the batch process without going through the LLM.
func PostToSlack(ctx context.Context, token, channel, text string) error {
	if token == "" || channel == "" {
		return fmt.Errorf("slack not configured: SLACK_BOT_TOKEN/SLACK_CHANNEL_ID unset")
	}
	client := slack.New(token)
	_, _, err := client.PostMessageContext(ctx, channel, slack.MsgOptionText(text, false))
	return err
}
