// Package slackfmt builds Slack Block Kit ([slack.Block]) messages, shared by
// every notification path in this repository (mail-triage summaries, LLM
// cost reports, and the Slack @mention listener's replies). It centers on
// the structure common to those callers -- a header, an optional fields
// summary, one or more body sections, and a context footnote -- while
// respecting Slack's per-message limits (50 blocks, 3000 characters per
// section, 150 characters per header).
package slackfmt

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

// maxHeaderChars is Slack's limit for a header block's plain_text.
const maxHeaderChars = 150

// maxSectionChars bounds a single section block's mrkdwn text, kept under
// Slack's 3000-character limit.
const maxSectionChars = 2900

// MaxBlocks is Slack's per-message block limit, enforced by Limit.
const MaxBlocks = 50

// Header returns a header block rendering text as a single bold heading line.
// Emoji shortcodes (e.g. ":mailbox_with_mail:") are rendered as emoji. text
// is truncated to Slack's header length limit if necessary.
func Header(text string) slack.Block {
	return slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, truncate(text, maxHeaderChars), true, false))
}

// Fields returns a fields-only section block from alternating label/value
// pairs (label1, value1, label2, value2, ...), rendered by Slack as a
// multi-column summary. A trailing unpaired label is dropped.
func Fields(pairs ...string) slack.Block {
	var fields []*slack.TextBlockObject
	for i := 0; i+1 < len(pairs); i += 2 {
		fields = append(fields, slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*%s*\n%s", pairs[i], pairs[i+1]), false, false))
	}
	return slack.NewSectionBlock(nil, fields, nil)
}

// Sections splits text into one or more mrkdwn section blocks, each within
// Slack's per-section character limit. It returns nil for empty text. Splits
// prefer a newline boundary within the chunk window so lines are not cut
// mid-line; otherwise they back off to the nearest UTF-8 rune boundary so a
// multi-byte character is never split in half.
func Sections(text string) []slack.Block {
	if text == "" {
		return nil
	}
	var blocks []slack.Block
	for len(text) > 0 {
		chunk := text
		if len(chunk) > maxSectionChars {
			chunk = truncate(text, maxSectionChars)
			if idx := strings.LastIndexByte(chunk, '\n'); idx > 0 {
				chunk = chunk[:idx]
			}
		}
		blocks = append(blocks, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, chunk, false, false), nil, nil))
		text = strings.TrimPrefix(text[len(chunk):], "\n")
	}
	return blocks
}

// Context returns a context block rendering text in Slack's small, muted
// style, for footnotes and disclaimers set apart from the main content.
func Context(text string) slack.Block {
	return slack.NewContextBlock("", slack.NewTextBlockObject(slack.MarkdownType, text, false, false))
}

// Divider returns a horizontal divider block.
func Divider() slack.Block {
	return slack.NewDividerBlock()
}

// Escape escapes the characters Slack's mrkdwn format treats specially in
// text (& < >). It must not be applied to the raw URL half of a <url|text>
// link.
func Escape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// CapItems returns at most max items from items, plus the count of items
// omitted, so a caller can render "...and N more" instead of an unbounded
// list that could blow past Slack's block/character limits.
func CapItems[T any](items []T, max int) (kept []T, omitted int) {
	if len(items) <= max {
		return items, 0
	}
	return items[:max], len(items) - max
}

// Limit trims blocks to at most MaxBlocks entries, replacing anything past
// the limit with a single context block noting the omission. Callers should
// apply this last, after assembling all blocks, as a safety net against
// Slack's per-message block limit.
func Limit(blocks []slack.Block) []slack.Block {
	if len(blocks) <= MaxBlocks {
		return blocks
	}
	kept := make([]slack.Block, MaxBlocks)
	copy(kept, blocks[:MaxBlocks-1])
	kept[MaxBlocks-1] = Context("…以下省略（表示上限のため）")
	return kept
}

// ColoredAttachment wraps blocks in a Slack attachment with a colored side
// bar (color is a Slack attachment color: a named value like
// "danger"/"warning"/"good" or a "#RRGGBB" hex string), for passing to
// slack.MsgOptionAttachments. Block Kit itself has no color-bar concept, so
// this is the only way to make a message visually stand out (e.g. an alert
// threshold being exceeded); it relies on the legacy attachments API
// alongside Block Kit.
func ColoredAttachment(color string, blocks ...slack.Block) slack.Attachment {
	return slack.Attachment{Color: color, Blocks: slack.Blocks{BlockSet: blocks}}
}

// truncate returns the prefix of s that is at most max bytes long, backing
// off to the nearest rune boundary so a multi-byte UTF-8 character is never
// split in half.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}
