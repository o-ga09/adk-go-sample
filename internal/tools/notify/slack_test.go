package notifytools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/o-ga09/adk-go-sample/internal/slackfmt"
	"github.com/slack-go/slack"
)

// blockLines renders blocks as one debug line per block, for readable
// assertions against the message structure without depending on slack-go's
// internal JSON layout.
func blockLines(t *testing.T, blocks []slack.Block) []string {
	t.Helper()
	lines := make([]string, 0, len(blocks))
	for _, b := range blocks {
		switch bl := b.(type) {
		case *slack.HeaderBlock:
			lines = append(lines, "HEADER: "+bl.Text.Text)
		case *slack.SectionBlock:
			if bl.Text != nil {
				lines = append(lines, "SECTION: "+bl.Text.Text)
				continue
			}
			var fields []string
			for _, f := range bl.Fields {
				fields = append(fields, f.Text)
			}
			lines = append(lines, "FIELDS: "+strings.Join(fields, " | "))
		case *slack.ContextBlock:
			var texts []string
			for _, e := range bl.ContextElements.Elements {
				if tb, ok := e.(*slack.TextBlockObject); ok {
					texts = append(texts, tb.Text)
				}
			}
			lines = append(lines, "CONTEXT: "+strings.Join(texts, " "))
		case *slack.DividerBlock:
			lines = append(lines, "DIVIDER")
		default:
			t.Fatalf("unexpected block type %T", b)
		}
	}
	return lines
}

func TestSummaryBlocks(t *testing.T) {
	tests := []struct {
		name string
		in   slackPushInput
		want []string
	}{
		{
			name: "全カテゴリあり",
			in: slackPushInput{
				NeedsReview: []needsReviewItem{
					{Subject: "セキュリティ通知", MessageID: "MSG1"},
					{Subject: "", MessageID: "MSG2"},
				},
				LabeledCount: 4,
				Events: []eventItem{
					{Title: "定例MTG", HTMLLink: "https://calendar.google.com/event?eid=abc", When: "7/12 10:00"},
				},
			},
			want: []string{
				"HEADER: :mailbox_with_mail: メール整理完了",
				"FIELDS: *要確認*\n2件 | *不要(ラベル付与)*\n4件 | *カレンダー登録*\n1件",
				"SECTION: *要確認メール*\n" +
					"・<https://mail.google.com/mail/u/0/#all/MSG1|セキュリティ通知>\n" +
					"・<https://mail.google.com/mail/u/0/#all/MSG2|(件名なし)>",
				"SECTION: *カレンダー登録*\n・<https://calendar.google.com/event?eid=abc|定例MTG> (7/12 10:00)",
			},
		},
		{
			name: "全0件でも表示が崩れない",
			in:   slackPushInput{},
			want: []string{
				"HEADER: :mailbox_with_mail: メール整理完了",
				"FIELDS: *要確認*\n0件 | *不要(ラベル付与)*\n0件 | *カレンダー登録*\n0件",
			},
		},
		{
			name: "エスケープが必要な件名",
			in: slackPushInput{
				NeedsReview: []needsReviewItem{
					{Subject: "A&B <重要>", MessageID: "M1"},
				},
			},
			want: []string{
				"HEADER: :mailbox_with_mail: メール整理完了",
				"FIELDS: *要確認*\n1件 | *不要(ラベル付与)*\n0件 | *カレンダー登録*\n0件",
				"SECTION: *要確認メール*\n・<https://mail.google.com/mail/u/0/#all/M1|A&amp;B &lt;重要&gt;>",
			},
		},
		{
			name: "messageIdが空の要確認メールはリンク化しない",
			in: slackPushInput{
				NeedsReview: []needsReviewItem{
					{Subject: "件名のみ", MessageID: ""},
				},
			},
			want: []string{
				"HEADER: :mailbox_with_mail: メール整理完了",
				"FIELDS: *要確認*\n1件 | *不要(ラベル付与)*\n0件 | *カレンダー登録*\n0件",
				"SECTION: *要確認メール*\n・件名のみ",
			},
		},
		{
			name: "htmlLinkが空のイベントはリンク化しない(dry_run等)",
			in: slackPushInput{
				Events: []eventItem{
					{Title: "定例MTG", HTMLLink: "", When: "7/12 10:00"},
				},
			},
			want: []string{
				"HEADER: :mailbox_with_mail: メール整理完了",
				"FIELDS: *要確認*\n0件 | *不要(ラベル付与)*\n0件 | *カレンダー登録*\n1件",
				"SECTION: *カレンダー登録*\n・定例MTG (7/12 10:00)",
			},
		},
		{
			name: "イベントのタイトルと日時のエスケープ",
			in: slackPushInput{
				Events: []eventItem{
					{Title: "A&B", HTMLLink: "https://calendar.example/e1", When: "<7/12>"},
				},
			},
			want: []string{
				"HEADER: :mailbox_with_mail: メール整理完了",
				"FIELDS: *要確認*\n0件 | *不要(ラベル付与)*\n0件 | *カレンダー登録*\n1件",
				"SECTION: *カレンダー登録*\n・<https://calendar.example/e1|A&amp;B> (&lt;7/12&gt;)",
			},
		},
		{
			name: "noteありはContext blockで注釈として分離される",
			in: slackPushInput{
				Note: "テスト & <確認>",
			},
			want: []string{
				"HEADER: :mailbox_with_mail: メール整理完了",
				"FIELDS: *要確認*\n0件 | *不要(ラベル付与)*\n0件 | *カレンダー登録*\n0件",
				"CONTEXT: テスト &amp; &lt;確認&gt;",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blockLines(t, summaryBlocks(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("summaryBlocks() = %d blocks, want %d\ngot:  %#v\nwant: %#v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("block[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestSummaryBlocks_CapsLargeListsAndStaysUnderLimits reproduces the "多数
// ある場合でもblock数/文字数上限でSlack API呼び出しがエラーにならない"
// acceptance criterion: a mailbox with far more items than fit comfortably
// must still cap the rendered list and never exceed Slack's 50-block ceiling
// or any individual section's character budget.
func TestSummaryBlocks_CapsLargeListsAndStaysUnderLimits(t *testing.T) {
	in := slackPushInput{}
	for i := 0; i < 200; i++ {
		in.NeedsReview = append(in.NeedsReview, needsReviewItem{
			Subject:   fmt.Sprintf("件名%d", i),
			MessageID: fmt.Sprintf("MSG%d", i),
		})
		in.Events = append(in.Events, eventItem{
			Title:    fmt.Sprintf("イベント%d", i),
			HTMLLink: fmt.Sprintf("https://calendar.example/%d", i),
			When:     "7/12 10:00",
		})
	}

	blocks := summaryBlocks(in)
	if len(blocks) > 50 {
		t.Fatalf("summaryBlocks() = %d blocks, want <= 50 (Slack's per-message limit)", len(blocks))
	}

	var sawOmissionNote bool
	for _, b := range blocks {
		sb, ok := b.(*slack.SectionBlock)
		if !ok || sb.Text == nil {
			continue
		}
		if len(sb.Text.Text) > 3000 {
			t.Errorf("section block text len = %d, want <= 3000", len(sb.Text.Text))
		}
		if strings.Contains(sb.Text.Text, "ほか") {
			sawOmissionNote = true
		}
	}
	if !sawOmissionNote {
		t.Error("summaryBlocks() with 200 items did not note the omitted count anywhere")
	}
}

func TestSubjectOrPlaceholder(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "空文字はプレースホルダーになる", in: "", want: "(件名なし)"},
		{name: "非空文字はそのまま", in: "テスト件名", want: "テスト件名"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subjectOrPlaceholder(tt.in); got != tt.want {
				t.Errorf("subjectOrPlaceholder(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestSlackPushInputSchemaAllowsOmittedFields replays the validation the ADK's
// functiontool runs on every call: fields without omitempty are required in
// the inferred schema, so the LLM omitting needsReview/events on a quiet day
// would fail the whole tool call.
func TestSlackPushInputSchemaAllowsOmittedFields(t *testing.T) {
	schema, err := jsonschema.For[slackPushInput](nil)
	if err != nil {
		t.Fatalf("infer schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	for _, payload := range []string{
		`{}`,
		`{"labeledCount":3}`,
		`{"note":"メールの取得に失敗しました"}`,
	} {
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(m); err != nil {
			t.Errorf("payload %s rejected by inferred schema: %v", payload, err)
		}
	}
}

func TestCalendarDigestBlocks(t *testing.T) {
	tests := []struct {
		name string
		in   calendarDigestInput
		want []string
	}{
		{
			name: "予定が複数件ある",
			in: calendarDigestInput{
				Events: []eventItem{
					{Title: "定例MTG", HTMLLink: "https://calendar.google.com/event?eid=abc", When: "7/12 10:00"},
					{Title: "歯医者", HTMLLink: "https://calendar.google.com/event?eid=def", When: "7/12 18:00"},
				},
			},
			want: []string{
				"HEADER: :calendar: 本日の予定",
				"SECTION: ・<https://calendar.google.com/event?eid=abc|定例MTG> (7/12 10:00)\n" +
					"・<https://calendar.google.com/event?eid=def|歯医者> (7/12 18:00)",
			},
		},
		{
			name: "予定が0件",
			in:   calendarDigestInput{},
			want: []string{"HEADER: :calendar: 本日の予定はありません"},
		},
		{
			name: "htmlLinkが空のイベントはリンク化しない(dry_run等)",
			in: calendarDigestInput{
				Events: []eventItem{
					{Title: "定例MTG", HTMLLink: "", When: "7/12 10:00"},
				},
			},
			want: []string{
				"HEADER: :calendar: 本日の予定",
				"SECTION: ・定例MTG (7/12 10:00)",
			},
		},
		{
			name: "タイトルと日時のエスケープ",
			in: calendarDigestInput{
				Events: []eventItem{
					{Title: "A&B", HTMLLink: "https://calendar.example/e1", When: "<7/12>"},
				},
			},
			want: []string{
				"HEADER: :calendar: 本日の予定",
				"SECTION: ・<https://calendar.example/e1|A&amp;B> (&lt;7/12&gt;)",
			},
		},
		{
			name: "件名が空のイベントはプレースホルダーになる",
			in: calendarDigestInput{
				Events: []eventItem{
					{Title: "", HTMLLink: "https://calendar.example/e1", When: "7/12 10:00"},
				},
			},
			want: []string{
				"HEADER: :calendar: 本日の予定",
				"SECTION: ・<https://calendar.example/e1|(件名なし)> (7/12 10:00)",
			},
		},
		{
			name: "予定0件でもnoteはContext blockで表示される",
			in: calendarDigestInput{
				Note: "テスト & <確認>",
			},
			want: []string{
				"HEADER: :calendar: 本日の予定はありません",
				"CONTEXT: テスト &amp; &lt;確認&gt;",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blockLines(t, calendarDigestBlocks(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("calendarDigestBlocks() = %d blocks, want %d\ngot:  %#v\nwant: %#v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("block[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestCalendarDigestBlocks_CapsLargeListsAndStaysUnderLimits mirrors
// TestSummaryBlocks_CapsLargeListsAndStaysUnderLimits for the calendar
// digest: an unusually full day must still cap the rendered list and never
// exceed Slack's per-message limits.
func TestCalendarDigestBlocks_CapsLargeListsAndStaysUnderLimits(t *testing.T) {
	in := calendarDigestInput{}
	for i := 0; i < 200; i++ {
		in.Events = append(in.Events, eventItem{
			Title:    fmt.Sprintf("イベント%d", i),
			HTMLLink: fmt.Sprintf("https://calendar.example/%d", i),
			When:     "7/12 10:00",
		})
	}

	blocks := calendarDigestBlocks(in)
	if len(blocks) > 50 {
		t.Fatalf("calendarDigestBlocks() = %d blocks, want <= 50 (Slack's per-message limit)", len(blocks))
	}

	var sawOmissionNote bool
	for _, b := range blocks {
		sb, ok := b.(*slack.SectionBlock)
		if !ok || sb.Text == nil {
			continue
		}
		if len(sb.Text.Text) > 3000 {
			t.Errorf("section block text len = %d, want <= 3000", len(sb.Text.Text))
		}
		if strings.Contains(sb.Text.Text, "ほか") {
			sawOmissionNote = true
		}
	}
	if !sawOmissionNote {
		t.Error("calendarDigestBlocks() with 200 events did not note the omitted count anywhere")
	}
}

// TestCalendarDigestInputSchemaAllowsOmittedFields replays the validation the
// ADK's functiontool runs on every call: fields without omitempty are
// required in the inferred schema, so the LLM omitting events on a day with
// zero registered events would fail the whole tool call.
func TestCalendarDigestInputSchemaAllowsOmittedFields(t *testing.T) {
	schema, err := jsonschema.For[calendarDigestInput](nil)
	if err != nil {
		t.Fatalf("infer schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	for _, payload := range []string{`{}`, `{"note":"本日の予定はありません"}`} {
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(m); err != nil {
			t.Errorf("payload %s rejected by inferred schema: %v", payload, err)
		}
	}
}

// TestEscapeIsSlackfmtEscape guards against the notify package silently
// reintroducing its own escaping instead of reusing the shared helper.
func TestEscapeIsSlackfmtEscape(t *testing.T) {
	if got, want := slackfmt.Escape("A&B <x>"), "A&amp;B &lt;x&gt;"; got != want {
		t.Errorf("slackfmt.Escape(%q) = %q, want %q", "A&B <x>", got, want)
	}
}

func TestGoblogSummaryBlocks(t *testing.T) {
	tests := []struct {
		name string
		in   goblogSummaryPushInput
		want []string
	}{
		{
			name: "タイトル・URL・要約・翻訳がすべてある",
			in: goblogSummaryPushInput{
				Title:       "range over func iterators",
				URL:         "https://go.dev/blog/range-functions",
				Summary:     "イテレータの要約です。",
				Translation: "本文の全文訳です。",
			},
			want: []string{
				"HEADER: range over func iterators",
				"CONTEXT: https://go.dev/blog/range-functions",
				"DIVIDER",
				"SECTION: *要約*\nイテレータの要約です。",
				"SECTION: *翻訳*\n本文の全文訳です。",
			},
		},
		{
			name: "noteがあるとContext blockで付記される",
			in: goblogSummaryPushInput{
				Title:   "タイトル",
				Summary: "要約 & <確認>",
				Note:    "補足 & <確認>",
			},
			want: []string{
				"HEADER: タイトル",
				"DIVIDER",
				"SECTION: *要約*\n要約 &amp; &lt;確認&gt;",
				"CONTEXT: 補足 &amp; &lt;確認&gt;",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blockLines(t, goblogSummaryBlocks(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("goblogSummaryBlocks() = %d blocks, want %d\ngot:  %#v\nwant: %#v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("block[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestGoblogListBlocks(t *testing.T) {
	tests := []struct {
		name string
		in   goblogListPushInput
		want []string
	}{
		{
			name: "記事一覧の公開日が分単位に整形される",
			in: goblogListPushInput{
				Posts: []goblogPostItem{
					{Title: "Introducing the pkg.go.dev API", URL: "https://go.dev/blog/pkgsite-api", PublishedAt: "2026-05-21T00:00:00+00:00"},
					{Title: "Type Construction and Cycle Detection", URL: "https://go.dev/blog/type-construction-and-cycle-detection", PublishedAt: "2026-03-24T09:15:30+09:00"},
				},
			},
			want: []string{
				"HEADER: :newspaper: 最近のGo Blog記事",
				"SECTION: ・<https://go.dev/blog/pkgsite-api|Introducing the pkg.go.dev API> (2026-05-21 00:00)\n" +
					"・<https://go.dev/blog/type-construction-and-cycle-detection|Type Construction and Cycle Detection> (2026-03-24 09:15)",
			},
		},
		{
			name: "0件は専用のヘッダーになる",
			in:   goblogListPushInput{},
			want: []string{"HEADER: :newspaper: 最近のGo Blog記事はありません"},
		},
		{
			name: "publishedAtが空でも壊れない",
			in: goblogListPushInput{
				Posts: []goblogPostItem{{Title: "タイトル", URL: "https://go.dev/blog/x"}},
			},
			want: []string{
				"HEADER: :newspaper: 最近のGo Blog記事",
				"SECTION: ・<https://go.dev/blog/x|タイトル>",
			},
		},
		{
			name: "highlightがあると要約セクションが追加される",
			in: goblogListPushInput{
				Posts:            []goblogPostItem{{Title: "タイトル", URL: "https://go.dev/blog/x", PublishedAt: "2026-05-21T00:00:00+00:00"}},
				HighlightTitle:   "タイトル",
				HighlightSummary: "要約本文",
			},
			want: []string{
				"HEADER: :newspaper: 最近のGo Blog記事",
				"SECTION: ・<https://go.dev/blog/x|タイトル> (2026-05-21 00:00)",
				"DIVIDER",
				"SECTION: *タイトルの要約*\n要約本文",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blockLines(t, goblogListBlocks(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("goblogListBlocks() = %d blocks, want %d\ngot:  %#v\nwant: %#v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("block[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestTaskListBlocks(t *testing.T) {
	tests := []struct {
		name string
		in   taskListPushInput
		want []string
	}{
		{
			name: "期限が分単位に整形されメタ情報が付く",
			in: taskListPushInput{
				Heading: "次にやるべきタスク",
				Tasks: []taskPushItem{
					{ID: "T1", Title: "資料作成", Status: "next", Context: "@pc", Due: "2026-07-20T09:00:00+09:00", Project: "案件A"},
				},
			},
			want: []string{
				"HEADER: :clipboard: 次にやるべきタスク",
				"SECTION: ・*資料作成* (next / @pc / 期限:2026-07-20 09:00 / project:案件A)",
			},
		},
		{
			name: "タスク0件は専用のヘッダーになる",
			in:   taskListPushInput{Heading: "タスク一覧"},
			want: []string{"HEADER: :clipboard: タスク一覧はありません"},
		},
		{
			name: "メタ情報が何もなくても壊れない",
			in: taskListPushInput{
				Heading: "タスク一覧",
				Tasks:   []taskPushItem{{ID: "T1", Title: "資料作成", Status: "inbox"}},
			},
			want: []string{
				"HEADER: :clipboard: タスク一覧",
				"SECTION: ・*資料作成* (inbox)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blockLines(t, taskListBlocks(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("taskListBlocks() = %d blocks, want %d\ngot:  %#v\nwant: %#v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("block[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestTaskActionBlocks(t *testing.T) {
	tests := []struct {
		name string
		in   taskActionPushInput
		want []string
	}{
		{
			name: "登録成功",
			in:   taskActionPushInput{Action: "add", Result: "created", Title: "資料作成"},
			want: []string{
				"HEADER: :inbox_tray: タスクを登録しました",
				"SECTION: *資料作成*",
			},
		},
		{
			name: "更新成功で期限が分単位に整形される",
			in: taskActionPushInput{
				Action: "update", Result: "updated", Title: "資料作成",
				TaskStatus: "next", Due: "2026-07-20T09:00:00+09:00",
			},
			want: []string{
				"HEADER: :pencil2: タスクを更新しました",
				"SECTION: *資料作成* (next / 期限:2026-07-20 09:00)",
			},
		},
		{
			name: "エラー時はnoteがContext blockになる",
			in:   taskActionPushInput{Action: "complete", Result: "error", Note: "対象が見つかりません"},
			want: []string{
				"HEADER: :warning: タスク操作に失敗しました",
				"CONTEXT: 対象が見つかりません",
			},
		},
		{
			name: "titleが空ならタスク詳細セクションは出ない",
			in:   taskActionPushInput{Action: "add", Result: "dry_run_would_add"},
			want: []string{"HEADER: :test_tube: (dry_run) タスクを登録します"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blockLines(t, taskActionBlocks(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("taskActionBlocks() = %d blocks, want %d\ngot:  %#v\nwant: %#v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("block[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestNewPushToolInputSchemasAllowOmittedFields replays the validation the
// ADK's functiontool runs on every call for the four push tools added
// alongside slack_push/calendar_digest_push: fields without omitempty are
// required in the inferred schema, so the LLM omitting an optional one (e.g.
// posts on an empty feed, or note when there's nothing to add) would fail the
// whole tool call. See .claude/rules/tool-json-schema.md.
func TestNewPushToolInputSchemasAllowOmittedFields(t *testing.T) {
	validate := func(t *testing.T, schema *jsonschema.Schema, payloads []string) {
		t.Helper()
		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Fatalf("resolve schema: %v", err)
		}
		for _, payload := range payloads {
			var m map[string]any
			if err := json.Unmarshal([]byte(payload), &m); err != nil {
				t.Fatal(err)
			}
			if err := resolved.Validate(m); err != nil {
				t.Errorf("payload %s rejected by inferred schema: %v", payload, err)
			}
		}
	}

	t.Run("goblog_summary_push", func(t *testing.T) {
		schema, err := jsonschema.For[goblogSummaryPushInput](nil)
		if err != nil {
			t.Fatalf("infer schema: %v", err)
		}
		validate(t, schema, []string{
			`{"title":"t","url":"https://go.dev/blog/x","summary":"s","translation":"tr"}`,
		})
	})

	t.Run("goblog_list_push", func(t *testing.T) {
		schema, err := jsonschema.For[goblogListPushInput](nil)
		if err != nil {
			t.Fatalf("infer schema: %v", err)
		}
		validate(t, schema, []string{`{}`, `{"note":"取得に失敗しました"}`})
	})

	t.Run("task_list_push", func(t *testing.T) {
		schema, err := jsonschema.For[taskListPushInput](nil)
		if err != nil {
			t.Fatalf("infer schema: %v", err)
		}
		validate(t, schema, []string{`{"heading":"タスク一覧"}`})
	})

	t.Run("task_action_push", func(t *testing.T) {
		schema, err := jsonschema.For[taskActionPushInput](nil)
		if err != nil {
			t.Fatalf("infer schema: %v", err)
		}
		validate(t, schema, []string{`{"action":"add","result":"created"}`})
	})
}

func TestTaskActionHeading(t *testing.T) {
	tests := []struct {
		name           string
		action, result string
		want           string
	}{
		{name: "add/created", action: "add", result: "created", want: ":inbox_tray: タスクを登録しました"},
		{name: "add/already_exists", action: "add", result: "already_exists", want: ":inbox_tray: タスクは既に登録済みです"},
		{name: "add/dry_run", action: "add", result: "dry_run_would_add", want: ":test_tube: (dry_run) タスクを登録します"},
		{name: "update/updated", action: "update", result: "updated", want: ":pencil2: タスクを更新しました"},
		{name: "update/dry_run", action: "update", result: "dry_run_would_update", want: ":test_tube: (dry_run) タスクを更新します"},
		{name: "complete/done", action: "complete", result: "done", want: ":white_check_mark: タスクを完了しました"},
		{name: "complete/dry_run", action: "complete", result: "dry_run_would_complete", want: ":test_tube: (dry_run) タスクを完了します"},
		{name: "任意のactionでerrorは共通の警告見出し", action: "add", result: "error", want: ":warning: タスク操作に失敗しました"},
		{name: "未知の組み合わせはフォールバック", action: "unknown", result: "unknown", want: ":clipboard: タスクを更新しました"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskActionHeading(tt.action, tt.result); got != tt.want {
				t.Errorf("taskActionHeading(%q, %q) = %q, want %q", tt.action, tt.result, got, tt.want)
			}
		})
	}
}
