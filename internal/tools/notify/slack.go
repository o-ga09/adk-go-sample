// Package notifytools (this file) exposes Slack notification tools backed by
// a Slack Bot Token (chat.postMessage). This is the sole notification channel
// used by the gmail agent to deliver its replies: every *_push tool here
// takes structured fields from the LLM and builds the Slack Block Kit
// message in Go, rather than asking the LLM to compose Slack-flavored
// Markdown (mrkdwn) or reformat timestamps itself.
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
// notification tool (#14); it and every other tool below share
// StatusSlackPushSent's "sent" value, so internal/slackbot's same
// delivered-check covers all of them.
const (
	ToolNameSlackPush          = "slack_push"
	ToolNameCalendarDigestPush = "calendar_digest_push"
	ToolNameGoblogSummaryPush  = "goblog_summary_push"
	ToolNameGoblogListPush     = "goblog_list_push"
	ToolNameTaskListPush       = "task_list_push"
	ToolNameTaskActionPush     = "task_action_push"
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

	goblogSummaryTool, err := functiontool.New(functiontool.Config{
		Name: ToolNameGoblogSummaryPush,
		Description: "Go blog記事の要約・翻訳結果をSlackへ通知する。title/url/summary(3〜5行の要約)/translation(本文の日本語全訳)を" +
			"渡すと、このツールが日本語のメッセージに整形してSlackへ投稿する。太字やリンクの記法・メッセージの整形はツール側で行うため、" +
			"呼び出し側で文面を組み立てる必要はない。goblog_fetch_postで記事を取得した後、一連の処理の最後に1回だけ呼び出すこと。",
	}, goblogSummaryPush(c))
	if err != nil {
		return nil, err
	}

	goblogListTool, err := functiontool.New(functiontool.Config{
		Name: ToolNameGoblogListPush,
		Description: "Go blogの最近の記事一覧をSlackへ通知する。goblog_list_postsで取得した各記事のtitle/url/publishedAt(RFC3339)を" +
			"postsにそのまま渡すこと(日時の書式変換や箇条書きの整形はツール側で行うため不要)。特定の記事を要約した場合はhighlightTitle/" +
			"highlightUrl/highlightSummaryにも渡すと、一覧の後にその要約も添えて投稿される。一連の処理の最後に1回だけ呼び出すこと。",
	}, goblogListPush(c))
	if err != nil {
		return nil, err
	}

	taskListTool, err := functiontool.New(functiontool.Config{
		Name: ToolNameTaskListPush,
		Description: "GTDタスクの一覧をSlackへ通知する。task_listで取得した各タスクのid/title/status/context/due(RFC3339)/projectを" +
			"tasksにそのまま渡すこと(日時の書式変換や箇条書きの整形はツール側で行うため不要)。headingには一覧の見出し" +
			"(例:「タスク一覧」「次にやるべきタスク」)を渡す。該当0件でもそのまま呼び出すこと。",
	}, taskListPush(c))
	if err != nil {
		return nil, err
	}

	taskActionTool, err := functiontool.New(functiontool.Config{
		Name: ToolNameTaskActionPush,
		Description: "task_add/task_update/task_completeの結果をSlackへ通知する。actionに\"add\"/\"update\"/\"complete\"のいずれか、" +
			"resultにその呼び出したツールが返したstatus文字列をそのまま渡す。わかっていればtitle/taskStatus/context/due(RFC3339)/" +
			"projectも渡すと日本語のメッセージに整形される(日時の書式変換は不要)。各操作の直後に1回だけ呼び出すこと。",
	}, taskActionPush(c))
	if err != nil {
		return nil, err
	}

	return []tool.Tool{pushTool, digestTool, goblogSummaryTool, goblogListTool, taskListTool, taskActionTool}, nil
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

func slackPush(c *config.Config) functiontool.Func[slackPushInput, pushResult] {
	client := slack.New(c.SlackBotToken)
	return func(ctx tool.Context, in slackPushInput) pushResult {
		return postBlocks(ctx, c, client, summaryBlocks(in))
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

func calendarDigestPush(c *config.Config) functiontool.Func[calendarDigestInput, pushResult] {
	client := slack.New(c.SlackBotToken)
	return func(ctx tool.Context, in calendarDigestInput) pushResult {
		return postBlocks(ctx, c, client, calendarDigestBlocks(in))
	}
}

// ---- goblog_summary_push ----

// goblogSummaryPushInput carries a fetched Go blog post's summary/translation
// as structured fields, so the message layout (heading, link, section
// labels) and any character-limit chunking are built in Go rather than
// relying on the LLM to write Slack-flavored Markdown itself.
type goblogSummaryPushInput struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Summary     string `json:"summary"`
	Translation string `json:"translation"`
	Note        string `json:"note,omitempty"`
}

func goblogSummaryPush(c *config.Config) functiontool.Func[goblogSummaryPushInput, pushResult] {
	client := slack.New(c.SlackBotToken)
	return func(ctx tool.Context, in goblogSummaryPushInput) pushResult {
		return postBlocks(ctx, c, client, goblogSummaryBlocks(in))
	}
}

// ---- goblog_list_push ----

// goblogPostItem is one entry from goblog_list_posts, passed through
// verbatim; publishedAt (RFC3339) is reformatted for display by
// slackfmt.FormatDateTime rather than by the LLM.
type goblogPostItem struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	PublishedAt string `json:"publishedAt,omitempty"`
}

// goblogListPushInput carries the post list plus an optional highlighted
// article's summary (when the caller also asked for one article's content).
// Highlight fields are flattened (rather than a nested optional struct) to
// keep the inferred JSON schema simple; see .claude/rules/tool-json-schema.md.
type goblogListPushInput struct {
	Posts            []goblogPostItem `json:"posts,omitempty"`
	HighlightTitle   string           `json:"highlightTitle,omitempty"`
	HighlightURL     string           `json:"highlightUrl,omitempty"`
	HighlightSummary string           `json:"highlightSummary,omitempty"`
	Note             string           `json:"note,omitempty"`
}

func goblogListPush(c *config.Config) functiontool.Func[goblogListPushInput, pushResult] {
	client := slack.New(c.SlackBotToken)
	return func(ctx tool.Context, in goblogListPushInput) pushResult {
		return postBlocks(ctx, c, client, goblogListBlocks(in))
	}
}

// ---- task_list_push ----

// taskPushItem is one entry from task_list, passed through verbatim; due
// (RFC3339) is reformatted for display by slackfmt.FormatDateTime rather than
// by the LLM.
type taskPushItem struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Context string `json:"context,omitempty"`
	Due     string `json:"due,omitempty"`
	Project string `json:"project,omitempty"`
}

type taskListPushInput struct {
	Heading string         `json:"heading"`
	Tasks   []taskPushItem `json:"tasks,omitempty"`
	Note    string         `json:"note,omitempty"`
}

func taskListPush(c *config.Config) functiontool.Func[taskListPushInput, pushResult] {
	client := slack.New(c.SlackBotToken)
	return func(ctx tool.Context, in taskListPushInput) pushResult {
		return postBlocks(ctx, c, client, taskListBlocks(in))
	}
}

// ---- task_action_push ----

// taskActionPushInput reports the outcome of a single task_add/task_update/
// task_complete call. Result carries that tool's own status string verbatim
// (e.g. "created"/"already_exists"/"dry_run_would_add"/"error"), which
// taskActionHeading maps to a Japanese heading, so the mapping lives in Go
// rather than being re-derived by the LLM each time. TaskStatus is the task's
// GTD status (inbox/next/waiting/someday/done); it is separate from Result to
// avoid conflating "did the call succeed" with "what state is the task in".
type taskActionPushInput struct {
	Action     string `json:"action"`
	Result     string `json:"result"`
	Title      string `json:"title,omitempty"`
	TaskStatus string `json:"taskStatus,omitempty"`
	Context    string `json:"context,omitempty"`
	Due        string `json:"due,omitempty"`
	Project    string `json:"project,omitempty"`
	Note       string `json:"note,omitempty"`
}

func taskActionPush(c *config.Config) functiontool.Func[taskActionPushInput, pushResult] {
	client := slack.New(c.SlackBotToken)
	return func(ctx tool.Context, in taskActionPushInput) pushResult {
		return postBlocks(ctx, c, client, taskActionBlocks(in))
	}
}

// pushResult is the generic {status, error} result shared by every *_push
// tool in this file.
type pushResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// postBlocks posts blocks into the Slack thread/channel the current request
// came from (see requestOrigin), falling back to SLACK_CHANNEL_ID as a
// top-level message when there is no request-scoped thread (the batch/cron
// paths, or a REST API call with no Slack origin). Shared by every *_push
// tool in this file.
func postBlocks(ctx tool.Context, c *config.Config, client *slack.Client, blocks []slack.Block) pushResult {
	channel, threadTS := requestOrigin(ctx)
	if channel == "" {
		channel = c.SlackChannelID
	}
	if c.SlackBotToken == "" || channel == "" {
		log.Print("slack notify skipped: not configured")
		return pushResult{Status: "skipped", Error: "Slack not configured"}
	}
	opts := []slack.MsgOption{slack.MsgOptionBlocks(blocks...)}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	if _, _, err := client.PostMessageContext(ctx, channel, opts...); err != nil {
		return pushResult{Status: "error", Error: err.Error()}
	}
	return pushResult{Status: StatusSlackPushSent}
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

// goblogSummaryBlocks renders in as a Slack Block Kit message: a header with
// the article title, a context block linking to the source URL, a divider,
// then one section each for the summary and translation (each independently
// chunked by slackfmt.Sections, since a full article translation can exceed
// a single section's character limit).
func goblogSummaryBlocks(in goblogSummaryPushInput) []slack.Block {
	blocks := []slack.Block{slackfmt.Header(in.Title)}
	if in.URL != "" {
		blocks = append(blocks, slackfmt.Context(in.URL))
	}
	blocks = append(blocks, slackfmt.Divider())
	if in.Summary != "" {
		blocks = append(blocks, slackfmt.Sections("*要約*\n"+slackfmt.Escape(in.Summary))...)
	}
	if in.Translation != "" {
		blocks = append(blocks, slackfmt.Sections("*翻訳*\n"+slackfmt.Escape(in.Translation))...)
	}
	if in.Note != "" {
		blocks = append(blocks, slackfmt.Context(slackfmt.Escape(in.Note)))
	}
	return slackfmt.Limit(blocks)
}

// goblogListBlocks renders in as a Slack Block Kit message: a header, one
// capped section listing the posts (title linked to its URL, plus its
// published date reformatted to minute precision by slackfmt.FormatDateTime),
// and, if a highlighted article was summarized, a divider followed by its
// summary.
func goblogListBlocks(in goblogListPushInput) []slack.Block {
	if len(in.Posts) == 0 {
		blocks := []slack.Block{slackfmt.Header(":newspaper: 最近のGo Blog記事はありません")}
		if in.Note != "" {
			blocks = append(blocks, slackfmt.Context(slackfmt.Escape(in.Note)))
		}
		return slackfmt.Limit(blocks)
	}

	kept, omitted := slackfmt.CapItems(in.Posts, maxListItems)
	var b strings.Builder
	for i, p := range kept {
		if i > 0 {
			b.WriteString("\n")
		}
		title := slackfmt.Escape(subjectOrPlaceholder(p.Title))
		if when := slackfmt.FormatDateTime(p.PublishedAt); when != "" {
			fmt.Fprintf(&b, "・<%s|%s> (%s)", p.URL, title, when)
		} else {
			fmt.Fprintf(&b, "・<%s|%s>", p.URL, title)
		}
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "\n…ほか%d件", omitted)
	}

	blocks := []slack.Block{slackfmt.Header(":newspaper: 最近のGo Blog記事")}
	blocks = append(blocks, slackfmt.Sections(b.String())...)

	if in.HighlightSummary != "" {
		blocks = append(blocks, slackfmt.Divider())
		heading := in.HighlightTitle
		if heading == "" {
			heading = "要約"
		}
		text := fmt.Sprintf("*%sの要約*\n%s", slackfmt.Escape(heading), slackfmt.Escape(in.HighlightSummary))
		blocks = append(blocks, slackfmt.Sections(text)...)
	}

	if in.Note != "" {
		blocks = append(blocks, slackfmt.Context(slackfmt.Escape(in.Note)))
	}
	return slackfmt.Limit(blocks)
}

// taskListBlocks renders in as a Slack Block Kit message: a header using
// in.Heading, one capped section listing the tasks (title bolded, followed
// by whichever of status/context/due/project are non-empty, due reformatted
// to minute precision by slackfmt.FormatDateTime), and a context block for
// the free-text note.
func taskListBlocks(in taskListPushInput) []slack.Block {
	heading := in.Heading
	if heading == "" {
		heading = "タスク"
	}
	if len(in.Tasks) == 0 {
		blocks := []slack.Block{slackfmt.Header(":clipboard: " + heading + "はありません")}
		if in.Note != "" {
			blocks = append(blocks, slackfmt.Context(slackfmt.Escape(in.Note)))
		}
		return slackfmt.Limit(blocks)
	}

	kept, omitted := slackfmt.CapItems(in.Tasks, maxListItems)
	var b strings.Builder
	for i, t := range kept {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "・*%s*", slackfmt.Escape(subjectOrPlaceholder(t.Title)))
		if meta := taskMeta(t.Status, t.Context, t.Due, t.Project); meta != "" {
			fmt.Fprintf(&b, " (%s)", meta)
		}
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "\n…ほか%d件", omitted)
	}

	blocks := []slack.Block{slackfmt.Header(":clipboard: " + heading)}
	blocks = append(blocks, slackfmt.Sections(b.String())...)
	if in.Note != "" {
		blocks = append(blocks, slackfmt.Context(slackfmt.Escape(in.Note)))
	}
	return slackfmt.Limit(blocks)
}

// taskActionBlocks renders in as a Slack Block Kit message: a header chosen
// by taskActionHeading from the (action, result) pair, an optional section
// naming the task and its known status/context/due/project, and a context
// block for the free-text note (typically an error detail).
func taskActionBlocks(in taskActionPushInput) []slack.Block {
	blocks := []slack.Block{slackfmt.Header(taskActionHeading(in.Action, in.Result))}

	if in.Title != "" {
		text := "*" + slackfmt.Escape(in.Title) + "*"
		if meta := taskMeta(in.TaskStatus, in.Context, in.Due, in.Project); meta != "" {
			text += " (" + meta + ")"
		}
		blocks = append(blocks, slackfmt.Sections(text)...)
	}

	if in.Note != "" {
		blocks = append(blocks, slackfmt.Context(slackfmt.Escape(in.Note)))
	}
	return slackfmt.Limit(blocks)
}

// taskMeta joins whichever of status/context/due/project are non-empty into
// a single " / "-separated string for display alongside a task's title, so
// taskListBlocks and taskActionBlocks render tasks identically. due (RFC3339)
// is reformatted to minute precision by slackfmt.FormatDateTime.
func taskMeta(status, context, due, project string) string {
	var meta []string
	if status != "" {
		meta = append(meta, status)
	}
	if context != "" {
		meta = append(meta, slackfmt.Escape(context))
	}
	if due := slackfmt.FormatDateTime(due); due != "" {
		meta = append(meta, "期限:"+due)
	}
	if project != "" {
		meta = append(meta, "project:"+slackfmt.Escape(project))
	}
	return strings.Join(meta, " / ")
}

// taskActionHeading maps a task_action_push (action, result) pair to a
// Japanese heading with a status emoji, so this mapping lives once in Go
// instead of being re-derived by the LLM on every call. action is
// "add"/"update"/"complete"; result is the verbatim status string returned by
// task_add/task_update/task_complete respectively (see internal/tools/tasks).
func taskActionHeading(action, result string) string {
	switch action {
	case "add":
		switch result {
		case "created":
			return ":inbox_tray: タスクを登録しました"
		case "already_exists":
			return ":inbox_tray: タスクは既に登録済みです"
		case "dry_run_would_add":
			return ":test_tube: (dry_run) タスクを登録します"
		}
	case "update":
		switch result {
		case "updated":
			return ":pencil2: タスクを更新しました"
		case "dry_run_would_update":
			return ":test_tube: (dry_run) タスクを更新します"
		}
	case "complete":
		switch result {
		case "done":
			return ":white_check_mark: タスクを完了しました"
		case "dry_run_would_complete":
			return ":test_tube: (dry_run) タスクを完了します"
		}
	}
	if result == "error" {
		return ":warning: タスク操作に失敗しました"
	}
	return ":clipboard: タスクを更新しました"
}

// subjectOrPlaceholder returns s, or a placeholder if s is empty, for display
// in Slack.
func subjectOrPlaceholder(s string) string {
	if s == "" {
		return "(件名なし)"
	}
	return s
}
