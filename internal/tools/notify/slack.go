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

// ToolNameSlackPush is the registered name of the notification tool, and
// StatusSlackPushSent is its result status for a delivered message.
// internal/slackbot watches tool responses for this name/status pair to know
// whether the summary was already delivered into the requesting thread.
//
// ToolNameCalendarDigestPush is the registered name of the calendar-digest
// notification tool (#14); it shares StatusSlackPushSent's "sent" value, so
// internal/slackbot's same delivered-check covers both tools.
const (
	ToolNameSlackPush          = "slack_push"
	ToolNameCalendarDigestPush = "calendar_digest_push"
	StatusSlackPushSent        = "sent"
)

// Session-state keys under which internal/slackbot records where the
// triggering Slack request came from. When present, the summary is posted as
// a reply in that thread instead of top-level to SLACK_CHANNEL_ID, so the
// whole exchange for one request stays in one thread.
const (
	StateKeySlackChannel  = "slack_channel"
	StateKeySlackThreadTS = "slack_thread_ts"
)

// SlackTools returns the Slack notification tool.
func SlackTools(c *config.Config) ([]tool.Tool, error) {
	pushTool, err := functiontool.New(functiontool.Config{
		Name: ToolNameSlackPush,
		Description: "受信メール整理の結果をSlackへ通知する。要確認メールの一覧(件名とメッセージID)、" +
			"不要としてラベル付与した件数、カレンダーに登録した予定の一覧(タイトル・イベントのhtmlLink・表示用日時)を" +
			"渡すと、このツールが日本語のサマリメッセージを整形してSlackチャンネルに投稿する。" +
			"メッセージ本文の整形はツール側で行うため、呼び出し側で文面を組み立てる必要はない。" +
			"一連の処理の最後に1回だけ呼び出すこと。",
	}, slackPush(c))
	if err != nil {
		return nil, err
	}

	digestTool, err := functiontool.New(functiontool.Config{
		Name: ToolNameCalendarDigestPush,
		Description: "カレンダーの予定一覧をSlackへ通知する(予定確認への回答・朝のダイジェスト共通)。" +
			"calendar_list_eventsで取得した予定の一覧(タイトル・htmlLink・表示用日時)を渡すと、" +
			"このツールが日本語のメッセージに整形してSlackへ投稿する。予定が0件でもそのまま呼び出すこと" +
			"(「本日の予定はありません」等として届く)。一連の処理の最後に1回だけ呼び出すこと。",
	}, calendarDigestPush(c))
	if err != nil {
		return nil, err
	}

	return []tool.Tool{pushTool, digestTool}, nil
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

// Every field is optional: the inferred JSON schema marks fields without
// `omitempty` as required, and the ADK rejects the whole call if the LLM omits
// one (e.g. no events registered that day). See
// .claude/rules/tool-json-schema.md.
type slackPushInput struct {
	NeedsReview  []needsReviewItem `json:"needsReview,omitempty"`
	LabeledCount int               `json:"labeledCount,omitempty"`
	Events       []eventItem       `json:"events,omitempty"`
	Note         string            `json:"note,omitempty"`
}

type slackPushResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func slackPush(c *config.Config) functiontool.Func[slackPushInput, slackPushResult] {
	client := slack.New(c.SlackBotToken)
	return func(ctx tool.Context, in slackPushInput) slackPushResult {
		channel, threadTS := requestOrigin(ctx)
		if channel == "" {
			channel = c.SlackChannelID
		}
		if c.SlackBotToken == "" || channel == "" {
			log.Print("slack notify skipped: not configured")
			return slackPushResult{Status: "skipped", Error: "Slack not configured"}
		}
		opts := []slack.MsgOption{slack.MsgOptionText(formatSummary(in), false)}
		if threadTS != "" {
			opts = append(opts, slack.MsgOptionTS(threadTS))
		}
		if _, _, err := client.PostMessageContext(ctx, channel, opts...); err != nil {
			return slackPushResult{Status: "error", Error: err.Error()}
		}
		return slackPushResult{Status: StatusSlackPushSent}
	}
}

// calendarDigestInput carries the events calendar_list_events returned.
// Events must carry `omitempty`: a zero-event day is the common case, and a
// nil slice without it would fail the inferred-schema validation. See
// .claude/rules/tool-json-schema.md.
type calendarDigestInput struct {
	Events []eventItem `json:"events,omitempty"`
	Note   string      `json:"note,omitempty"`
}

type calendarDigestResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func calendarDigestPush(c *config.Config) functiontool.Func[calendarDigestInput, calendarDigestResult] {
	client := slack.New(c.SlackBotToken)
	return func(ctx tool.Context, in calendarDigestInput) calendarDigestResult {
		channel, threadTS := requestOrigin(ctx)
		if channel == "" {
			channel = c.SlackChannelID
		}
		if c.SlackBotToken == "" || channel == "" {
			log.Print("calendar digest notify skipped: not configured")
			return calendarDigestResult{Status: "skipped", Error: "Slack not configured"}
		}
		opts := []slack.MsgOption{slack.MsgOptionText(formatDigest(in), false)}
		if threadTS != "" {
			opts = append(opts, slack.MsgOptionTS(threadTS))
		}
		if _, _, err := client.PostMessageContext(ctx, channel, opts...); err != nil {
			return calendarDigestResult{Status: "error", Error: err.Error()}
		}
		return calendarDigestResult{Status: StatusSlackPushSent}
	}
}

// requestOrigin reads the Slack channel/thread the current request came from
// out of session state. Both values are empty for the batch and REST API
// paths, which never set them.
func requestOrigin(ctx tool.Context) (channel, threadTS string) {
	return stateString(ctx, StateKeySlackChannel), stateString(ctx, StateKeySlackThreadTS)
}

func stateString(ctx tool.Context, key string) string {
	v, err := ctx.State().Get(key)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
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

// formatDigest renders in as the Japanese Slack mrkdwn calendar digest
// message, e.g.:
//
//	:calendar: 本日の予定
//	・<HTMLLINK|定例MTG> (7/12 10:00)
//
// or, with zero events:
//
//	:calendar: 本日の予定はありません
func formatDigest(in calendarDigestInput) string {
	var b strings.Builder
	if len(in.Events) == 0 {
		b.WriteString(":calendar: 本日の予定はありません")
	} else {
		b.WriteString(":calendar: 本日の予定\n")
		for i, e := range in.Events {
			if i > 0 {
				b.WriteString("\n")
			}
			title := escapeMrkdwn(subjectOrPlaceholder(e.Title))
			when := escapeMrkdwn(e.When)
			if e.HTMLLink == "" {
				fmt.Fprintf(&b, "・%s (%s)", title, when)
				continue
			}
			fmt.Fprintf(&b, "・<%s|%s> (%s)", e.HTMLLink, title, when)
		}
	}
	if in.Note != "" {
		fmt.Fprintf(&b, "\n\n備考: %s", escapeMrkdwn(in.Note))
	}
	return truncate(b.String(), maxMessageLen)
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
