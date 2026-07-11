// Package notifytools (this file) exposes a Slack notification tool backed by
// a Slack Bot Token (chat.postMessage). This is the sole notification channel
// used by the gmail agent to deliver its summary.
package notifytools

import (
	"fmt"
	"log"
	"strings"
	"unicode/utf8"

	"github.com/o-ga09/adk-go-sample/internal/config"
	"github.com/slack-go/slack"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// gmailMessageLinkPrefix, followed by a Gmail message id, opens that message
// in the Gmail web UI.
const gmailMessageLinkPrefix = "https://mail.google.com/mail/u/0/#all/"

// maxMessageLen bounds the formatted Slack message length (Slack's own limit
// is ~40,000 characters for a single message).
const maxMessageLen = 39000

// SlackTools returns the Slack notification tool.
func SlackTools(c *config.Config) ([]tool.Tool, error) {
	pushTool, err := functiontool.New(functiontool.Config{
		Name: "slack_push",
		Description: "受信メール整理の結果をSlackへ通知する。要確認メールの一覧(件名とメッセージID)、" +
			"不要としてラベル付与した件数、カレンダーに登録した予定の一覧(タイトル・イベントのhtmlLink・表示用日時)を" +
			"渡すと、このツールが日本語のサマリメッセージを整形してSlackチャンネルに投稿する。" +
			"メッセージ本文の整形はツール側で行うため、呼び出し側で文面を組み立てる必要はない。" +
			"一連の処理の最後に1回だけ呼び出すこと。",
	}, slackPush(c))
	if err != nil {
		return nil, err
	}
	return []tool.Tool{pushTool}, nil
}

// needsReviewItem is a mail the agent judged the user should review.
type needsReviewItem struct {
	Subject   string `json:"subject"`
	MessageID string `json:"messageId"`
}

// eventItem is a calendar event the agent created from a mail.
type eventItem struct {
	Title    string `json:"title"`
	HTMLLink string `json:"htmlLink"`
	When     string `json:"when"`
}

type slackPushInput struct {
	NeedsReview  []needsReviewItem `json:"needsReview"`
	LabeledCount int               `json:"labeledCount"`
	Events       []eventItem       `json:"events"`
	Note         string            `json:"note,omitempty"`
}

type slackPushResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func slackPush(c *config.Config) functiontool.Func[slackPushInput, slackPushResult] {
	client := slack.New(c.SlackBotToken)
	return func(ctx tool.Context, in slackPushInput) slackPushResult {
		if c.SlackBotToken == "" || c.SlackChannelID == "" {
			log.Print("slack notify skipped: not configured")
			return slackPushResult{Status: "skipped", Error: "Slack not configured"}
		}
		text := formatSummary(in)
		if _, _, err := client.PostMessageContext(ctx, c.SlackChannelID, slack.MsgOptionText(text, false)); err != nil {
			return slackPushResult{Status: "error", Error: err.Error()}
		}
		return slackPushResult{Status: "sent"}
	}
}

// formatSummary renders in as the Japanese Slack mrkdwn summary message,
// e.g.:
//
//	:mailbox_with_mail: メール整理完了
//	・要確認: 1件
//	　- <https://mail.google.com/mail/u/0/#all/MSGID|セキュリティ通知>
//	・不要(ラベル付与): 4件
//	・カレンダー登録: 1件
//	　- <HTMLLINK|定例MTG> (7/12 10:00)
func formatSummary(in slackPushInput) string {
	var b strings.Builder
	b.WriteString(":mailbox_with_mail: メール整理完了\n")

	fmt.Fprintf(&b, "・要確認: %d件\n", len(in.NeedsReview))
	for _, m := range in.NeedsReview {
		subject := escapeMrkdwn(subjectOrPlaceholder(m.Subject))
		if m.MessageID == "" {
			// No message id to link to (shouldn't normally happen, but avoid
			// emitting a broken "<|subject>" mrkdwn link).
			fmt.Fprintf(&b, "　- %s\n", subject)
			continue
		}
		link := gmailMessageLinkPrefix + m.MessageID
		fmt.Fprintf(&b, "　- <%s|%s>\n", link, subject)
	}

	fmt.Fprintf(&b, "・不要(ラベル付与): %d件\n", in.LabeledCount)

	fmt.Fprintf(&b, "・カレンダー登録: %d件\n", len(in.Events))
	for _, e := range in.Events {
		title := escapeMrkdwn(subjectOrPlaceholder(e.Title))
		when := escapeMrkdwn(e.When)
		if e.HTMLLink == "" {
			// calendar_create_event returns no htmlLink for dry_run and
			// already_exists results; fall back to plain text instead of an
			// empty-URL "<|title>" mrkdwn link.
			fmt.Fprintf(&b, "　- %s (%s)\n", title, when)
			continue
		}
		fmt.Fprintf(&b, "　- <%s|%s> (%s)\n", e.HTMLLink, title, when)
	}

	if in.Note != "" {
		fmt.Fprintf(&b, "\n備考: %s\n", escapeMrkdwn(in.Note))
	}

	return truncate(strings.TrimRight(b.String(), "\n"), maxMessageLen)
}

// subjectOrPlaceholder returns s, or a placeholder if s is empty, for display
// in Slack.
func subjectOrPlaceholder(s string) string {
	if s == "" {
		return "(件名なし)"
	}
	return s
}

// escapeMrkdwn escapes the characters Slack's mrkdwn format treats specially
// in link text (& < >). It must not be applied to the raw URL half of a
// <url|text> link.
func escapeMrkdwn(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// truncate returns the prefix of s that is at most max bytes long, backing
// off to the nearest rune boundary so a multi-byte UTF-8 character (e.g.
// Japanese text) is never split in half.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}
