// Package notifytools (this file) exposes a Slack notification tool backed by
// a Slack Bot Token (chat.postMessage). This is the sole notification channel
// used by the gmail agent to deliver its summary.
package notifytools

import (
	"fmt"
	"log"
	"strings"

	"github.com/o-ga09/adk-go-sample/internal/config"
	"github.com/o-ga09/adk-go-sample/internal/slackfmt"
	"github.com/slack-go/slack"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// gmailMessageLinkPrefix, followed by a Gmail message id, opens that message
// in the Gmail web UI.
const gmailMessageLinkPrefix = "https://mail.google.com/mail/u/0/#all/"

// maxListItems bounds how many needsReview/events entries are individually
// listed in the summary message, so a very large batch can't blow past
// Slack's per-message block/character limits (see .claude/rules and
// slackfmt.Limit, applied as a final safety net regardless).
const maxListItems = 20

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
		opts := []slack.MsgOption{slack.MsgOptionBlocks(summaryBlocks(in)...)}
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
		opts := []slack.MsgOption{slack.MsgOptionBlocks(calendarDigestBlocks(in)...)}
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

// summaryBlocks renders in as a Slack Block Kit summary message: a header,
// a fields block with the three headline counts, one section per non-empty
// list (要確認メール / カレンダー登録, each capped at maxListItems with a
// "…ほかN件" note so an unusually large batch can't blow past Slack's
// per-message limits), and a context block for the free-text note.
func summaryBlocks(in slackPushInput) []slack.Block {
	blocks := []slack.Block{
		slackfmt.Header(":mailbox_with_mail: メール整理完了"),
		slackfmt.Fields(
			"要確認", fmt.Sprintf("%d件", len(in.NeedsReview)),
			"不要(ラベル付与)", fmt.Sprintf("%d件", in.LabeledCount),
			"カレンダー登録", fmt.Sprintf("%d件", len(in.Events)),
		),
	}

	if len(in.NeedsReview) > 0 {
		blocks = append(blocks, renderCappedList("要確認メール", in.NeedsReview, maxListItems, func(m needsReviewItem) string {
			subject := slackfmt.Escape(subjectOrPlaceholder(m.Subject))
			if m.MessageID == "" {
				// No message id to link to (shouldn't normally happen, but avoid
				// emitting a broken "<|subject>" mrkdwn link).
				return "・" + subject
			}
			return fmt.Sprintf("・<%s%s|%s>", gmailMessageLinkPrefix, m.MessageID, subject)
		})...)
	}

	if len(in.Events) > 0 {
		blocks = append(blocks, renderCappedList("カレンダー登録", in.Events, maxListItems, func(e eventItem) string {
			title := slackfmt.Escape(subjectOrPlaceholder(e.Title))
			when := slackfmt.Escape(e.When)
			if e.HTMLLink == "" {
				// calendar_create_event returns no htmlLink for dry_run and
				// already_exists results; fall back to plain text instead of an
				// empty-URL "<|title>" mrkdwn link.
				return fmt.Sprintf("・%s (%s)", title, when)
			}
			return fmt.Sprintf("・<%s|%s> (%s)", e.HTMLLink, title, when)
		})...)
	}

	if in.Note != "" {
		blocks = append(blocks, slackfmt.Context(slackfmt.Escape(in.Note)))
	}

	return slackfmt.Limit(blocks)
}

// renderCappedList renders heading followed by up to max items (formatted
// one per line via line), noting how many were omitted beyond that, as one
// or more mrkdwn section blocks. Shared by the NeedsReview and Events lists
// in summaryBlocks, which differ only in per-item formatting.
func renderCappedList[T any](heading string, items []T, max int, line func(T) string) []slack.Block {
	kept, omitted := slackfmt.CapItems(items, max)
	var b strings.Builder
	fmt.Fprintf(&b, "*%s*\n", heading)
	for _, it := range kept {
		b.WriteString(line(it))
		b.WriteString("\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "…ほか%d件\n", omitted)
	}
	return slackfmt.Sections(strings.TrimRight(b.String(), "\n"))
}

// calendarDigestBlocks renders in as a Slack Block Kit calendar digest
// message: a header (either "本日の予定" or, for zero events, "本日の予定は
// ありません" so that case needs no body section), one capped section
// listing the events (mirroring renderCappedList's "…ほかN件" omission note
// for an unusually large day), and a context block for the free-text note.
func calendarDigestBlocks(in calendarDigestInput) []slack.Block {
	if len(in.Events) == 0 {
		blocks := []slack.Block{slackfmt.Header(":calendar: 本日の予定はありません")}
		if in.Note != "" {
			blocks = append(blocks, slackfmt.Context(slackfmt.Escape(in.Note)))
		}
		return slackfmt.Limit(blocks)
	}

	kept, omitted := slackfmt.CapItems(in.Events, maxListItems)
	var b strings.Builder
	for i, e := range kept {
		if i > 0 {
			b.WriteString("\n")
		}
		title := slackfmt.Escape(subjectOrPlaceholder(e.Title))
		when := slackfmt.Escape(e.When)
		if e.HTMLLink == "" {
			fmt.Fprintf(&b, "・%s (%s)", title, when)
			continue
		}
		fmt.Fprintf(&b, "・<%s|%s> (%s)", e.HTMLLink, title, when)
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "\n…ほか%d件", omitted)
	}

	blocks := []slack.Block{slackfmt.Header(":calendar: 本日の予定")}
	blocks = append(blocks, slackfmt.Sections(b.String())...)
	if in.Note != "" {
		blocks = append(blocks, slackfmt.Context(slackfmt.Escape(in.Note)))
	}
	return slackfmt.Limit(blocks)
}

// subjectOrPlaceholder returns s, or a placeholder if s is empty, for display
// in Slack.
func subjectOrPlaceholder(s string) string {
	if s == "" {
		return "(件名なし)"
	}
	return s
}
