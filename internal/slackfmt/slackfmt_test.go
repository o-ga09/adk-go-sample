package slackfmt

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

func TestHeader(t *testing.T) {
	got := Header(":mailbox_with_mail: メール整理完了")
	hb, ok := got.(*slack.HeaderBlock)
	if !ok {
		t.Fatalf("Header() type = %T, want *slack.HeaderBlock", got)
	}
	if hb.Text == nil || hb.Text.Type != slack.PlainTextType {
		t.Fatalf("Header() text type = %+v, want plain_text", hb.Text)
	}
	if hb.Text.Text != ":mailbox_with_mail: メール整理完了" {
		t.Errorf("Header() text = %q, want unchanged short text", hb.Text.Text)
	}
	if hb.Text.Emoji == nil || !*hb.Text.Emoji {
		t.Error("Header() text.Emoji = false, want true so :emoji: shortcodes render")
	}
}

func TestHeader_TruncatesLongText(t *testing.T) {
	// Slack rejects header plain_text over 150 characters.
	long := strings.Repeat("a", 300)
	got := Header(long).(*slack.HeaderBlock)
	if len(got.Text.Text) > maxHeaderChars {
		t.Fatalf("Header() text len = %d, want <= %d", len(got.Text.Text), maxHeaderChars)
	}
	if !utf8.ValidString(got.Text.Text) {
		t.Error("Header() truncated text is not valid UTF-8")
	}
}

func TestFields(t *testing.T) {
	got := Fields("要確認", "1件", "不要(ラベル付与)", "4件").(*slack.SectionBlock)
	if got.Text != nil {
		t.Errorf("Fields() Text = %+v, want nil (fields-only section)", got.Text)
	}
	if len(got.Fields) != 2 {
		t.Fatalf("Fields() len = %d, want 2", len(got.Fields))
	}
	if got.Fields[0].Text != "*要確認*\n1件" {
		t.Errorf("Fields()[0] = %q, want %q", got.Fields[0].Text, "*要確認*\n1件")
	}
	if got.Fields[1].Text != "*不要(ラベル付与)*\n4件" {
		t.Errorf("Fields()[1] = %q, want %q", got.Fields[1].Text, "*不要(ラベル付与)*\n4件")
	}
	for _, f := range got.Fields {
		if f.Type != slack.MarkdownType {
			t.Errorf("field type = %q, want mrkdwn", f.Type)
		}
	}
}

func TestFields_OddPairIgnoresTrailingLabel(t *testing.T) {
	got := Fields("要確認", "1件", "danglingLabel").(*slack.SectionBlock)
	if len(got.Fields) != 1 {
		t.Fatalf("Fields() len = %d, want 1 (trailing unpaired label dropped)", len(got.Fields))
	}
}

func TestSections_Empty(t *testing.T) {
	if got := Sections(""); got != nil {
		t.Errorf("Sections(\"\") = %v, want nil", got)
	}
}

func TestSections_ShortTextIsOneBlock(t *testing.T) {
	got := Sections("・要確認: 1件\n・不要: 4件")
	if len(got) != 1 {
		t.Fatalf("Sections() len = %d, want 1", len(got))
	}
	sb, ok := got[0].(*slack.SectionBlock)
	if !ok {
		t.Fatalf("Sections()[0] type = %T, want *slack.SectionBlock", got[0])
	}
	if sb.Text.Text != "・要確認: 1件\n・不要: 4件" {
		t.Errorf("Sections()[0] text = %q", sb.Text.Text)
	}
	if sb.Text.Type != slack.MarkdownType {
		t.Errorf("Sections()[0] text type = %q, want mrkdwn", sb.Text.Type)
	}
}

func TestSections_SplitsLongTextUnderCharLimit(t *testing.T) {
	long := strings.Repeat("a", maxSectionChars*2+10)
	got := Sections(long)
	if len(got) < 2 {
		t.Fatalf("Sections() len = %d, want >= 2 for text longer than the section limit", len(got))
	}
	var rebuilt strings.Builder
	for i, b := range got {
		sb, ok := b.(*slack.SectionBlock)
		if !ok {
			t.Fatalf("Sections()[%d] type = %T, want *slack.SectionBlock", i, b)
		}
		if len(sb.Text.Text) > maxSectionChars {
			t.Errorf("Sections()[%d] len = %d, want <= %d", i, len(sb.Text.Text), maxSectionChars)
		}
		rebuilt.WriteString(sb.Text.Text)
	}
	if rebuilt.String() != long {
		t.Error("Sections() chunks do not reconstruct the original text")
	}
}

func TestSections_SplitsOnNewlineWhenPossible(t *testing.T) {
	// One line comfortably short, followed by a very long second line: the
	// split should happen at the newline rather than mid-line.
	long := "short line\n" + strings.Repeat("b", maxSectionChars)
	got := Sections(long)
	if len(got) < 2 {
		t.Fatalf("Sections() len = %d, want >= 2", len(got))
	}
	first := got[0].(*slack.SectionBlock).Text.Text
	if first != "short line" {
		t.Errorf("Sections()[0] = %q, want %q (split at newline)", first, "short line")
	}
}

func TestSections_MultiByteRuneBoundarySafe(t *testing.T) {
	long := strings.Repeat("あ", maxSectionChars) // 3 bytes/rune, guaranteed to straddle the byte cutoff somewhere
	got := Sections(long)
	var rebuilt strings.Builder
	for i, b := range got {
		text := b.(*slack.SectionBlock).Text.Text
		if !utf8.ValidString(text) {
			t.Errorf("Sections()[%d] = %q is not valid UTF-8", i, text)
		}
		rebuilt.WriteString(text)
	}
	if rebuilt.String() != long {
		t.Error("Sections() chunks do not reconstruct the original multi-byte text")
	}
}

func TestContext(t *testing.T) {
	got := Context("※ 単価テーブルによる推定値です").(*slack.ContextBlock)
	if len(got.ContextElements.Elements) != 1 {
		t.Fatalf("Context() elements = %d, want 1", len(got.ContextElements.Elements))
	}
	tb, ok := got.ContextElements.Elements[0].(*slack.TextBlockObject)
	if !ok {
		t.Fatalf("Context() element type = %T, want *slack.TextBlockObject", got.ContextElements.Elements[0])
	}
	if tb.Text != "※ 単価テーブルによる推定値です" || tb.Type != slack.MarkdownType {
		t.Errorf("Context() text = %+v", tb)
	}
}

func TestDivider(t *testing.T) {
	if _, ok := Divider().(*slack.DividerBlock); !ok {
		t.Errorf("Divider() type = %T, want *slack.DividerBlock", Divider())
	}
}

func TestEscape(t *testing.T) {
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
			if got := Escape(tt.in); got != tt.want {
				t.Errorf("Escape(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCapItems(t *testing.T) {
	tests := []struct {
		name        string
		items       []int
		max         int
		wantKept    []int
		wantOmitted int
	}{
		{name: "上限以下はそのまま", items: []int{1, 2}, max: 5, wantKept: []int{1, 2}, wantOmitted: 0},
		{name: "上限ちょうどはそのまま", items: []int{1, 2, 3}, max: 3, wantKept: []int{1, 2, 3}, wantOmitted: 0},
		{name: "上限超過は切り詰めて件数を返す", items: []int{1, 2, 3, 4, 5}, max: 2, wantKept: []int{1, 2}, wantOmitted: 3},
		{name: "空スライス", items: nil, max: 2, wantKept: nil, wantOmitted: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept, omitted := CapItems(tt.items, tt.max)
			if len(kept) != len(tt.wantKept) {
				t.Fatalf("CapItems() kept = %v, want %v", kept, tt.wantKept)
			}
			for i := range kept {
				if kept[i] != tt.wantKept[i] {
					t.Errorf("CapItems() kept[%d] = %v, want %v", i, kept[i], tt.wantKept[i])
				}
			}
			if omitted != tt.wantOmitted {
				t.Errorf("CapItems() omitted = %d, want %d", omitted, tt.wantOmitted)
			}
		})
	}
}

func TestLimit_UnderMaxUnchanged(t *testing.T) {
	blocks := []slack.Block{Divider(), Divider()}
	got := Limit(blocks)
	if len(got) != 2 {
		t.Fatalf("Limit() len = %d, want 2", len(got))
	}
}

func TestLimit_OverMaxTrimsAndNotes(t *testing.T) {
	blocks := make([]slack.Block, MaxBlocks+10)
	for i := range blocks {
		blocks[i] = Divider()
	}
	got := Limit(blocks)
	if len(got) != MaxBlocks {
		t.Fatalf("Limit() len = %d, want %d", len(got), MaxBlocks)
	}
	last, ok := got[len(got)-1].(*slack.ContextBlock)
	if !ok {
		t.Fatalf("Limit() last block type = %T, want *slack.ContextBlock noting the omission", got[len(got)-1])
	}
	_ = last
}

func TestFormatDateTime(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "秒とオフセットを落として分単位にする", in: "2026-05-21T09:30:45+09:00", want: "2026-05-21 09:30"},
		{name: "UTCオフセットでも同様", in: "2026-05-21T00:00:00+00:00", want: "2026-05-21 00:00"},
		{name: "空文字は空文字のまま", in: "", want: ""},
		{name: "RFC3339でない値はそのまま返す(サイレントに握りつぶさない)", in: "2026-05-21", want: "2026-05-21"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatDateTime(tt.in); got != tt.want {
				t.Errorf("FormatDateTime(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestColoredAttachment(t *testing.T) {
	got := ColoredAttachment("danger", Divider(), Divider())
	if got.Color != "danger" {
		t.Errorf("ColoredAttachment() Color = %q, want %q", got.Color, "danger")
	}
	if len(got.Blocks.BlockSet) != 2 {
		t.Fatalf("ColoredAttachment() Blocks = %d, want 2", len(got.Blocks.BlockSet))
	}
	for i, b := range got.Blocks.BlockSet {
		if _, ok := b.(*slack.DividerBlock); !ok {
			t.Errorf("ColoredAttachment() Blocks[%d] type = %T, want *slack.DividerBlock", i, b)
		}
	}
}
