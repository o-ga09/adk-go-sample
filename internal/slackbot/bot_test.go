package slackbot

import (
	"testing"

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
