// Package gmailagent builds the personal secretary LLM agent: it classifies
// inbox mail, labels unwanted mail, registers calendar events, and notifies
// the user over Slack (via the Slack Bot Token / chat.postMessage API). It
// also fetches, summarizes, and translates Go blog posts when asked to
// (typically via the Slack @mention listener).
package gmailagent

import (
	"context"
	"fmt"

	"github.com/o-ga09/adk-go-sample/internal/config"
	googleapi "github.com/o-ga09/adk-go-sample/internal/google"
	calendartools "github.com/o-ga09/adk-go-sample/internal/tools/calendar"
	gmailtools "github.com/o-ga09/adk-go-sample/internal/tools/gmail"
	goblogtools "github.com/o-ga09/adk-go-sample/internal/tools/goblog"
	notifytools "github.com/o-ga09/adk-go-sample/internal/tools/notify"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// Instruction guides the agent. The {gmail_query} / {action_mode} placeholders
// are resolved from session state at runtime by ADK.
//
// The agent is invoked both by the cron batch (always the fixed "受信トレイを
// 整理して通知してください" request) and, since the Slack integration, by
// arbitrary @mention text from the user. The instruction therefore routes
// between two tasks based on what the message actually asks for.
const instructionTmpl = `あなたはユーザー専用の秘書エージェントです。依頼内容に応じて、以下のどちらか一方の業務だけを行ってください。

# 業務A: 受信メールの整理・通知(「受信トレイを整理して」等、メール整理の依頼のとき)
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
5. 最後に slack_push を1回だけ呼び、結果を通知する。メッセージ文面はツールが整形するため、
   以下の構造化引数をそのまま渡すこと:
   - needsReview: 「要確認」と分類した各メールの subject(件名)と messageId(id)の配列。
   - labeledCount: 「不要」としてラベル付与した件数。
   - events: タイトル(title)と表示用の日時(when、例: "7/12 10:00")は自分が calendar_create_event に
     渡した値(summary等)を使い、htmlLinkはcalendar_create_eventの戻り値のものを使う。戻り値のhtmlLinkが
     空(dry_runやalready_existsの場合)ならhtmlLinkは空のまま渡してよい。
   - note: ツールがエラーを返した場合など、要約に含めるべき補足があれば1〜2行で。無ければ省略してよい。

# 業務B: Go blogの要約・翻訳(https://go.dev/blog/ のURLを渡され、要約または翻訳を頼まれたとき)
1. goblog_fetch_post にそのURLを渡し、記事のタイトルと本文を取得する。
2. 取得した内容をもとに、次の2つを日本語であなたの返信メッセージにそのまま書く(slack_push は使わない。対話的な依頼への返信は、あなたの最終応答テキストがそのままSlackスレッドに投稿される):
   - 要約: 3〜5行程度で記事の要点をまとめる。
   - 翻訳: 記事本文の日本語全文訳。
3. URLが https://go.dev/ 以外、または goblog_fetch_post がエラーを返した場合は、その旨を日本語で簡潔に伝える。

# 注意
- 業務Aの現在の動作モード: %s。dry_run の場合、ツールは実際の変更を行わずログのみ返すが、手順は同じように実行すること。
- ツールがエラーを返したら、業務Aでは slack_push の note 引数に、業務Bでは返信にその旨を含める。無限ループを避け、各メール・各記事の処理は一度だけにする。
- どちらの業務にも当てはまらない依頼の場合は、対応できない旨を日本語で伝える。`

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
	stools, err := notifytools.SlackTools(cfg.App)
	if err != nil {
		return nil, fmt.Errorf("slack notify tools: %w", err)
	}
	btools, err := goblogtools.Tools()
	if err != nil {
		return nil, fmt.Errorf("go blog tools: %w", err)
	}

	allTools := make([]tool.Tool, 0, len(gtools)+len(ctools)+len(stools)+len(btools))
	allTools = append(allTools, gtools...)
	allTools = append(allTools, ctools...)
	allTools = append(allTools, stools...)
	allTools = append(allTools, btools...)

	instruction := fmt.Sprintf(instructionTmpl, cfg.App.GmailQuery, mode)

	return llmagent.New(llmagent.Config{
		Name:        cfg.App.AppName,
		Model:       cfg.Model,
		Description: "受信メールを整理(分類・ラベル付け)し、予定をカレンダー登録し、要約をSlack通知する秘書エージェント。Go blogのURLを渡すと要約・翻訳も行う。",
		Instruction: instruction,
		Tools:       allTools,
	})
}
