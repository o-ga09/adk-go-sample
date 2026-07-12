package notifytools

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestFormatSummary(t *testing.T) {
	tests := []struct {
		name string
		in   slackPushInput
		want string
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
			want: ":mailbox_with_mail: メール整理完了\n" +
				"・要確認: 2件\n" +
				"　- <https://mail.google.com/mail/u/0/#all/MSG1|セキュリティ通知>\n" +
				"　- <https://mail.google.com/mail/u/0/#all/MSG2|(件名なし)>\n" +
				"・不要(ラベル付与): 4件\n" +
				"・カレンダー登録: 1件\n" +
				"　- <https://calendar.google.com/event?eid=abc|定例MTG> (7/12 10:00)",
		},
		{
			name: "全0件",
			in:   slackPushInput{},
			want: ":mailbox_with_mail: メール整理完了\n" +
				"・要確認: 0件\n" +
				"・不要(ラベル付与): 0件\n" +
				"・カレンダー登録: 0件",
		},
		{
			name: "エスケープが必要な件名",
			in: slackPushInput{
				NeedsReview: []needsReviewItem{
					{Subject: "A&B <重要>", MessageID: "M1"},
				},
			},
			want: ":mailbox_with_mail: メール整理完了\n" +
				"・要確認: 1件\n" +
				"　- <https://mail.google.com/mail/u/0/#all/M1|A&amp;B &lt;重要&gt;>\n" +
				"・不要(ラベル付与): 0件\n" +
				"・カレンダー登録: 0件",
		},
		{
			name: "件名が空",
			in: slackPushInput{
				NeedsReview: []needsReviewItem{
					{Subject: "", MessageID: "M2"},
				},
			},
			want: ":mailbox_with_mail: メール整理完了\n" +
				"・要確認: 1件\n" +
				"　- <https://mail.google.com/mail/u/0/#all/M2|(件名なし)>\n" +
				"・不要(ラベル付与): 0件\n" +
				"・カレンダー登録: 0件",
		},
		{
			name: "イベントのタイトルと日時のエスケープ",
			in: slackPushInput{
				Events: []eventItem{
					{Title: "A&B", HTMLLink: "https://calendar.example/e1", When: "<7/12>"},
				},
			},
			want: ":mailbox_with_mail: メール整理完了\n" +
				"・要確認: 0件\n" +
				"・不要(ラベル付与): 0件\n" +
				"・カレンダー登録: 1件\n" +
				"　- <https://calendar.example/e1|A&amp;B> (&lt;7/12&gt;)",
		},
		{
			name: "noteあり（エスケープも適用される）",
			in: slackPushInput{
				Note: "テスト & <確認>",
			},
			want: ":mailbox_with_mail: メール整理完了\n" +
				"・要確認: 0件\n" +
				"・不要(ラベル付与): 0件\n" +
				"・カレンダー登録: 0件\n" +
				"\n備考: テスト &amp; &lt;確認&gt;",
		},
		{
			name: "messageIdが空の要確認メールはリンク化しない",
			in: slackPushInput{
				NeedsReview: []needsReviewItem{
					{Subject: "件名のみ", MessageID: ""},
				},
			},
			want: ":mailbox_with_mail: メール整理完了\n" +
				"・要確認: 1件\n" +
				"　- 件名のみ\n" +
				"・不要(ラベル付与): 0件\n" +
				"・カレンダー登録: 0件",
		},
		{
			name: "htmlLinkが空のイベントはリンク化しない(dry_run等)",
			in: slackPushInput{
				Events: []eventItem{
					{Title: "定例MTG", HTMLLink: "", When: "7/12 10:00"},
				},
			},
			want: ":mailbox_with_mail: メール整理完了\n" +
				"・要確認: 0件\n" +
				"・不要(ラベル付与): 0件\n" +
				"・カレンダー登録: 1件\n" +
				"　- 定例MTG (7/12 10:00)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSummary(tt.in)
			if got != tt.want {
				t.Errorf("formatSummary() mismatch\n got:  %q\n want: %q", got, tt.want)
			}
		})
	}
}

func TestFormatSummary_Truncation(t *testing.T) {
	// Build a note long enough (multi-byte throughout) that the untruncated
	// message exceeds maxMessageLen, and that the raw maxMessageLen byte
	// offset lands in the middle of a multi-byte rune, so a naive s[:max]
	// byte slice (the old, buggy behavior) would produce invalid UTF-8.
	longNote := strings.Repeat("あ", maxMessageLen)
	in := slackPushInput{Note: longNote}
	untruncated := ":mailbox_with_mail: メール整理完了\n" +
		"・要確認: 0件\n" +
		"・不要(ラベル付与): 0件\n" +
		"・カレンダー登録: 0件\n" +
		"\n備考: " + longNote

	got := formatSummary(in)

	if len(got) > maxMessageLen {
		t.Fatalf("formatSummary() len = %d, want <= %d", len(got), maxMessageLen)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("formatSummary() produced invalid UTF-8: %q", got)
	}
	if !strings.HasPrefix(untruncated, got) {
		t.Fatalf("formatSummary() result is not a prefix of the untruncated message")
	}
	// The cut must back off no further than one rune's worth of bytes from
	// maxMessageLen, otherwise truncate is trimming more than necessary to
	// respect a rune boundary.
	if len(got) <= maxMessageLen-utf8.UTFMax {
		t.Errorf("formatSummary() truncated further than a rune boundary requires: len = %d, maxMessageLen = %d", len(got), maxMessageLen)
	}
	if len(got) == len(untruncated) {
		t.Fatalf("formatSummary() did not truncate at all; test input is not long enough")
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

func TestEscapeMrkdwn(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "アンパサンド", in: "A&B", want: "A&amp;B"},
		{name: "山括弧", in: "<A>", want: "&lt;A&gt;"},
		{name: "複合", in: "A&B <重要>", want: "A&amp;B &lt;重要&gt;"},
		{name: "特殊文字なし", in: "テスト", want: "テスト"},
		{name: "空文字", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeMrkdwn(tt.in); got != tt.want {
				t.Errorf("escapeMrkdwn(%q) = %q, want %q", tt.in, got, tt.want)
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

func TestFormatDigest(t *testing.T) {
	tests := []struct {
		name string
		in   calendarDigestInput
		want string
	}{
		{
			name: "予定が複数件ある",
			in: calendarDigestInput{
				Events: []eventItem{
					{Title: "定例MTG", HTMLLink: "https://calendar.google.com/event?eid=abc", When: "7/12 10:00"},
					{Title: "歯医者", HTMLLink: "https://calendar.google.com/event?eid=def", When: "7/12 18:00"},
				},
			},
			want: ":calendar: 本日の予定\n" +
				"・<https://calendar.google.com/event?eid=abc|定例MTG> (7/12 10:00)\n" +
				"・<https://calendar.google.com/event?eid=def|歯医者> (7/12 18:00)",
		},
		{
			name: "予定が0件",
			in:   calendarDigestInput{},
			want: ":calendar: 本日の予定はありません",
		},
		{
			name: "htmlLinkが空のイベントはリンク化しない(dry_run等)",
			in: calendarDigestInput{
				Events: []eventItem{
					{Title: "定例MTG", HTMLLink: "", When: "7/12 10:00"},
				},
			},
			want: ":calendar: 本日の予定\n" +
				"・定例MTG (7/12 10:00)",
		},
		{
			name: "タイトルと日時のエスケープ",
			in: calendarDigestInput{
				Events: []eventItem{
					{Title: "A&B", HTMLLink: "https://calendar.example/e1", When: "<7/12>"},
				},
			},
			want: ":calendar: 本日の予定\n" +
				"・<https://calendar.example/e1|A&amp;B> (&lt;7/12&gt;)",
		},
		{
			name: "件名が空のイベントはプレースホルダーになる",
			in: calendarDigestInput{
				Events: []eventItem{
					{Title: "", HTMLLink: "https://calendar.example/e1", When: "7/12 10:00"},
				},
			},
			want: ":calendar: 本日の予定\n" +
				"・<https://calendar.example/e1|(件名なし)> (7/12 10:00)",
		},
		{
			name: "noteあり（エスケープも適用される）",
			in: calendarDigestInput{
				Note: "テスト & <確認>",
			},
			want: ":calendar: 本日の予定はありません\n" +
				"\n備考: テスト &amp; &lt;確認&gt;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDigest(tt.in)
			if got != tt.want {
				t.Errorf("formatDigest() mismatch\n got:  %q\n want: %q", got, tt.want)
			}
		})
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

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "上限未満はそのまま", in: "hello", max: 10, want: "hello"},
		{name: "上限ちょうどはそのまま", in: "hello", max: 5, want: "hello"},
		{name: "上限超過は切り詰め", in: "hello", max: 4, want: "hell"},
		{name: "上限0で空文字を返す", in: "hello", max: 0, want: ""},
		{name: "空文字入力", in: "", max: 5, want: ""},
		// "こんにちは" is 5 runes of 3 bytes each (UTF-8), boundaries at
		// byte offsets 0, 3, 6, 9, 12, 15.
		{name: "マルチバイト: 上限がルーン境界の途中は前の境界まで戻す(1バイト目)", in: "こんにちは", max: 4, want: "こ"},
		{name: "マルチバイト: 上限がルーン境界の途中は前の境界まで戻す(2バイト目)", in: "こんにちは", max: 5, want: "こ"},
		{name: "マルチバイト: 上限がちょうどルーン境界ならそこまで残す", in: "こんにちは", max: 6, want: "こん"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.in, tt.max)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("truncate(%q, %d) = %q is not valid UTF-8", tt.in, tt.max, got)
			}
		})
	}
}
