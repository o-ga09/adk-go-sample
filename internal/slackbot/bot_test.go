package slackbot

import (
	"strings"
	"testing"

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
			lines = append(lines, "SECTION: "+bl.Text.Text)
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

func TestReplyBlocks_PlainTextHasNoTitle(t *testing.T) {
	got := blockLines(t, replyBlocks("", "", "ご用件をメンションの後に書いてください。"))
	want := []string{"SECTION: ご用件をメンションの後に書いてください。"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("replyBlocks() = %v, want %v", got, want)
	}
}

func TestReplyBlocks_WithTitleAndURLSeparatesMetaFromBody(t *testing.T) {
	got := blockLines(t, replyBlocks("range over func iterators", "https://go.dev/blog/range-functions", "この記事はイテレータについて..."))
	want := []string{
		"HEADER: range over func iterators",
		"CONTEXT: https://go.dev/blog/range-functions",
		"DIVIDER",
		"SECTION: この記事はイテレータについて...",
	}
	if len(got) != len(want) {
		t.Fatalf("replyBlocks() = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("block[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReplyBlocks_WithTitleButNoURL(t *testing.T) {
	got := blockLines(t, replyBlocks("タイトルのみ", "", "本文"))
	want := []string{"HEADER: タイトルのみ", "DIVIDER", "SECTION: 本文"}
	if len(got) != len(want) {
		t.Fatalf("replyBlocks() = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("block[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReplyBlocks_LongBodyStaysUnderBlockLimit(t *testing.T) {
	long := strings.Repeat("あ", 20000) // far more than fits in one 3000-char section
	blocks := replyBlocks("長い記事", "https://go.dev/blog/long", long)
	if len(blocks) > slackfmt.MaxBlocks {
		t.Fatalf("replyBlocks() = %d blocks, want <= %d", len(blocks), slackfmt.MaxBlocks)
	}
	for _, b := range blocks {
		if sb, ok := b.(*slack.SectionBlock); ok && len(sb.Text.Text) > 3000 {
			t.Errorf("section block text len = %d, want <= 3000", len(sb.Text.Text))
		}
	}
}
