// Package gmailagent builds the Gmail-secretary LLM agent: it classifies inbox
// mail, labels unwanted mail, registers calendar events, and notifies the user
// over LINE.
package gmailagent

import (
	"context"
	"fmt"

	"github.com/o-ga09/adk-go-sample/internal/config"
	googleapi "github.com/o-ga09/adk-go-sample/internal/google"
	calendartools "github.com/o-ga09/adk-go-sample/internal/tools/calendar"
	gmailtools "github.com/o-ga09/adk-go-sample/internal/tools/gmail"
	notifytools "github.com/o-ga09/adk-go-sample/internal/tools/notify"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// Instruction guides the agent. The {gmail_query} / {action_mode} placeholders
// are resolved from session state at runtime by ADK.
const instructionTmpl = `あなたはユーザー専用の秘書エージェントです。受信メールを整理し、結果をLINEで通知します。

# 手順
1. gmail_list_messages を query="%s" で呼び、対象メールの一覧を取得する。
2. 各メールについて gmail_get_message で本文を確認し、次の3カテゴリに分類する:
   - 要確認: ユーザーが目を通すべき重要・個人的なメール。
   - 不要: 広告・宣伝・通知など、読まなくてよいメール。
   - 予定あり: 日時の決まった予定(会議・予約・締切など)を含むメール。
3. 「不要」と判断したメールには、gmail_ensure_label で『秘書/不要』ラベルを用意し、
   gmail_apply_label でそのラベルを付与する(removeFromInbox=true)。メールは削除しない。
4. 「予定あり」のメールは、calendar_create_event で予定を登録する。
   - startRFC3339 / endRFC3339 は RFC3339 形式(タイムゾーンは +09:00)。終了時刻が不明なら開始の1時間後。
   - srcMessageId にそのメールの id を渡し、重複登録を防ぐ。
5. 最後に line_push で日本語の要約を1通だけ送る。形式の例:
   「📬 メール整理完了
   ・要確認: N件 (件名を箇条書き)
   ・不要(ラベル付与): M件
   ・カレンダー登録: K件 (予定名と日時)」

# 注意
- 現在の動作モード: %s。dry_run の場合、ツールは実際の変更を行わずログのみ返すが、手順は同じように実行すること。
- ツールがエラーを返したら、その旨を要約に含める。無限ループを避け、各メールの処理は一度だけにする。`

// Config carries the dependencies needed to build the agent.
type Config struct {
	Model   model.LLM
	Clients *googleapi.Clients
	App     *config.Config
}

// New builds the gmail secretary agent.
func New(ctx context.Context, cfg Config) (agent.Agent, error) {
	mode := cfg.App.ActionMode

	gtools, err := gmailtools.Tools(cfg.Clients.Gmail, mode)
	if err != nil {
		return nil, fmt.Errorf("gmail tools: %w", err)
	}
	ctools, err := calendartools.Tools(cfg.Clients.Calendar, mode)
	if err != nil {
		return nil, fmt.Errorf("calendar tools: %w", err)
	}
	ntools, err := notifytools.Tools(cfg.App)
	if err != nil {
		return nil, fmt.Errorf("notify tools: %w", err)
	}

	allTools := make([]tool.Tool, 0, len(gtools)+len(ctools)+len(ntools))
	allTools = append(allTools, gtools...)
	allTools = append(allTools, ctools...)
	allTools = append(allTools, ntools...)

	instruction := fmt.Sprintf(instructionTmpl, cfg.App.GmailQuery, mode)

	return llmagent.New(llmagent.Config{
		Name:        cfg.App.AppName,
		Model:       cfg.Model,
		Description: "受信メールを整理(分類・ラベル付け)し、予定をカレンダー登録し、要約をLINE通知する秘書エージェント。",
		Instruction: instruction,
		Tools:       allTools,
	})
}
