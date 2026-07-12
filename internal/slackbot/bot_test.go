package slackbot

import (
	"strings"
	"testing"

	"github.com/o-ga09/adk-go-sample/internal/slackfmt"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/stretchr/testify/assert"
)

func TestIncomingMessage_ThreadKeyAndSessionID(t *testing.T) {
	tests := []struct {
		name    string
		msg     incomingMessage
		wantKey string
		wantSID string
	}{
		{
			name:    "正常系: スレッド内の場合はThreadTimeStampを使う",
			msg:     incomingMessage{Channel: "C1", TimeStamp: "111.000", ThreadTimeStamp: "100.000"},
			wantKey: "100.000",
			wantSID: "slack-C1-100.000",
		},
		{
			name:    "正常系: スレッド外(単発メンション)の場合はTimeStampにフォールバックする",
			msg:     incomingMessage{Channel: "C1", TimeStamp: "111.000", ThreadTimeStamp: ""},
			wantKey: "111.000",
			wantSID: "slack-C1-111.000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantKey, tt.msg.threadKey())
			assert.Equal(t, tt.wantSID, tt.msg.sessionID())
		})
	}
}

func TestParseAppMention(t *testing.T) {
	tests := []struct {
		name     string
		mention  *slackevents.AppMentionEvent
		wantOK   bool
		wantMsg  incomingMessage
		wantText string
	}{
		{
			name: "正常系: メンション本文からメンショントークンを取り除く",
			mention: &slackevents.AppMentionEvent{
				User: "U1", Channel: "C1", TimeStamp: "111.000", ThreadTimeStamp: "100.000",
				Text: "<@BOTID> 受信トレイを整理して",
			},
			wantOK:   true,
			wantMsg:  incomingMessage{User: "U1", Channel: "C1", TimeStamp: "111.000", ThreadTimeStamp: "100.000"},
			wantText: "受信トレイを整理して",
		},
		{
			name: "正常系: メンション本文が空の場合はテキスト空のまま返す(呼び出し元で案内文を出す)",
			mention: &slackevents.AppMentionEvent{
				User: "U1", Channel: "C1", TimeStamp: "111.000",
				Text: "<@BOTID>   ",
			},
			wantOK:   true,
			wantMsg:  incomingMessage{User: "U1", Channel: "C1", TimeStamp: "111.000"},
			wantText: "",
		},
		{
			name: "異常系: 他ボットが発火したapp_mentionは無視する",
			mention: &slackevents.AppMentionEvent{
				User: "U1", Channel: "C1", TimeStamp: "111.000", BotID: "B1",
				Text: "<@BOTID> hi",
			},
			wantOK: false,
		},
		{
			name:   "異常系: nilイベントは無視する",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, text, ok := parseAppMention(tt.mention)
			assert.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				return
			}
			assert.Equal(t, tt.wantMsg, msg)
			assert.Equal(t, tt.wantText, text)
		})
	}
}

func TestParseThreadReply(t *testing.T) {
	const botUserID = "BOTID"

	tests := []struct {
		name     string
		ev       *slackevents.MessageEvent
		wantOK   bool
		wantMsg  incomingMessage
		wantText string
	}{
		{
			name: "正常系: スレッド内の素のメッセージを拾う",
			ev: &slackevents.MessageEvent{
				User: "U1", Channel: "C1", TimeStamp: "111.000", ThreadTimeStamp: "100.000",
				Text: "続きだけどこれも整理して",
			},
			wantOK:   true,
			wantMsg:  incomingMessage{User: "U1", Channel: "C1", TimeStamp: "111.000", ThreadTimeStamp: "100.000"},
			wantText: "続きだけどこれも整理して",
		},
		{
			name: "異常系: ボット自身の投稿(bot_id有り)は無視する",
			ev: &slackevents.MessageEvent{
				User: "U1", Channel: "C1", TimeStamp: "111.000", ThreadTimeStamp: "100.000",
				Text: "hi", BotID: "B1",
			},
			wantOK: false,
		},
		{
			name: "異常系: message_changed等のsubtypeは無視する",
			ev: &slackevents.MessageEvent{
				User: "U1", Channel: "C1", TimeStamp: "111.000", ThreadTimeStamp: "100.000",
				Text: "hi", SubType: "message_changed",
			},
			wantOK: false,
		},
		{
			name: "異常系: スレッド外の通常メッセージは無視する",
			ev: &slackevents.MessageEvent{
				User: "U1", Channel: "C1", TimeStamp: "111.000",
				Text: "hi",
			},
			wantOK: false,
		},
		{
			name: "異常系: スレッドの起点メッセージ自体は無視する",
			ev: &slackevents.MessageEvent{
				User: "U1", Channel: "C1", TimeStamp: "100.000", ThreadTimeStamp: "100.000",
				Text: "hi",
			},
			wantOK: false,
		},
		{
			name: "異常系: ボットへの再メンションはapp_mention側に任せて無視する",
			ev: &slackevents.MessageEvent{
				User: "U1", Channel: "C1", TimeStamp: "111.000", ThreadTimeStamp: "100.000",
				Text: "<@BOTID> もう一度お願い",
			},
			wantOK: false,
		},
		{
			name: "異常系: 空白のみの本文は無視する",
			ev: &slackevents.MessageEvent{
				User: "U1", Channel: "C1", TimeStamp: "111.000", ThreadTimeStamp: "100.000",
				Text: "   ",
			},
			wantOK: false,
		},
		{
			name:   "異常系: nilイベントは無視する",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, text, ok := parseThreadReply(tt.ev, botUserID)
			assert.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				return
			}
			assert.Equal(t, tt.wantMsg, msg)
			assert.Equal(t, tt.wantText, text)
		})
	}
}

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
