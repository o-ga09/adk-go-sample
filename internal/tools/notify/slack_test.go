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
