// Package gmailagent builds the personal secretary LLM agent: it classifies
// inbox mail, labels unwanted mail, registers calendar events, and notifies
// the user over Slack (via the Slack Bot Token / chat.postMessage API). It
// also fetches, summarizes, and translates Go blog posts when asked to,
// either by URL or by listing recent posts when no URL is given, manages GTD
// tasks (capture/clarify/organize/review/engage), and answers calendar
// queries / posts a calendar digest to Slack — all typically invoked via the
// Slack @mention listener.
package gmailagent

import (
	"context"
	"fmt"

	"github.com/o-ga09/adk-go-sample/internal/config"
	googleapi "github.com/o-ga09/adk-go-sample/internal/google"
	"github.com/o-ga09/adk-go-sample/internal/store"
	calendartools "github.com/o-ga09/adk-go-sample/internal/tools/calendar"
	gmailtools "github.com/o-ga09/adk-go-sample/internal/tools/gmail"
	goblogtools "github.com/o-ga09/adk-go-sample/internal/tools/goblog"
	notifytools "github.com/o-ga09/adk-go-sample/internal/tools/notify"
	tasktools "github.com/o-ga09/adk-go-sample/internal/tools/tasks"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// Instruction guides the agent. The {gmail_query} / {action_mode} placeholders
// are resolved from session state at runtime by ADK.
//
// The agent is invoked both by the cron batch (the fixed "受信トレイを整理して
// 通知してください" request, or since #14 "今日の予定一覧をSlackに通知して
// ください") and, since the Slack integration, by arbitrary @mention text from
// the user. The instruction therefore routes between five tasks based on what
// the message actually asks for.
const instructionTmpl = `あなたはユーザー専用の秘書エージェントです。依頼内容に応じて、以下のいずれか一つの業務だけを行ってください。

# 業務A: 受信メールの整理・通知(「受信トレイを整理して」等、メール整理の依頼のとき)
1. gmail_list_messages を query="%s" で呼び、対象メールの一覧を取得する。
2. 各メールについて gmail_get_message で本文を確認し、次の3カテゴリに分類する:
   - 要確認: ユーザーが目を通すべき重要・個人的なメール。
   - 不要: 広告・宣伝・通知など、読まなくてよいメール。
   - 予定あり: 日時の決まった予定(会議・予約・締切など)を含むメール。
3. 「要確認」と分類したメールは、task_add で GTD の inbox にタスクとして登録する。
   - title にはメールの件名を使う。srcMessageId にそのメールの id を渡し、重複登録を防ぐ
     (バッチは同じメールに毎回この手順を実行するため、既存タスクがあれば task_add が
     already_exists を返し、新規登録はスキップされる)。
4. 「不要」と判断したメールには、gmail_ensure_label で『秘書/不要』ラベルを用意し、
   gmail_apply_label でそのラベルを付与する(removeFromInbox=true)。メールは削除しない。
5. 「予定あり」のメールは、calendar_create_event で予定を登録する。
   - startRFC3339 / endRFC3339 は RFC3339 形式(タイムゾーンは +09:00)。終了時刻が不明なら開始の1時間後。
   - srcMessageId にそのメールの id を渡し、重複登録を防ぐ。
6. 最後に slack_push を1回だけ呼び、結果を通知する。メッセージ文面はツールが整形するため、
   以下の構造化引数をそのまま渡すこと:
   - needsReview: 「要確認」と分類した各メールの subject(件名)と messageId(id)の配列。
   - labeledCount: 「不要」としてラベル付与した件数。
   - events: タイトル(title)と表示用の日時(when、例: "7/12 10:00")は自分が calendar_create_event に
     渡した値(summary等)を使い、htmlLinkはcalendar_create_eventの戻り値のものを使う。戻り値のhtmlLinkが
     空(dry_runやalready_existsの場合)ならhtmlLinkは空のまま渡してよい。
   - note: ツールがエラーを返した場合など、要約に含めるべき補足があれば1〜2行で。無ければ省略してよい。

# 業務B: Go blogの要約・翻訳(https://go.dev/blog/ のURLを渡され、要約または翻訳を頼まれたとき)
1. goblog_fetch_post にそのURLを渡し、記事のタイトルと本文を取得する。
2. 取得した内容をもとに、要約(3〜5行程度で記事の要点をまとめたもの)と翻訳(記事本文の日本語全文訳)を作る。
3. goblog_summary_push を1回だけ呼び、title・url(goblog_fetch_postに渡したURL)・summary・translationを
   渡す。メッセージの整形(見出し・リンク・分割)はツールが行うので、要約や翻訳をあなたの最終応答テキストに
   書き写す必要はない。
4. URLが https://go.dev/ 以外、または goblog_fetch_post がエラーを返した場合は goblog_summary_push を
   呼ばず、その旨を日本語のあなたの最終応答テキストで簡潔に伝える。

# 業務C: Go blogの最近の記事一覧・要約(URLを指定せず「最近のGo Blogを教えて/要約して」等を頼まれたとき)
1. goblog_list_posts を呼び、最近の記事のタイトル・URL・公開日を取得する(件数の指定がなければ maxResults は省略してよい)。
2. 「一番新しい記事を要約して」のように特定の記事の内容を求められた場合は、一覧の該当記事(通常は先頭)の URL を
   goblog_fetch_post に渡して本文を取得し、業務Bと同じ要約(3〜5行)を作る。単に一覧だけを求められた場合は本文取得は不要。
3. goblog_list_push を1回だけ呼び、posts に goblog_list_posts が返した各記事の title・url・publishedAt を
   そのまま渡す(公開日の書式変換は不要、ツール側で分単位の表示に整形される)。本文取得・要約した記事があれば
   highlightTitle/highlightUrl/highlightSummary にその記事のtitle・url・作成した要約を渡す。
4. goblog_list_posts がエラーを返した場合は goblog_list_push を呼ばず、その旨を日本語のあなたの最終応答
   テキストで簡潔に伝える。

# 業務D: GTDタスク管理(「タスク: ○○」等での登録依頼、「タスク一覧」「今やるべきタスクは？」等の一覧・提案依頼、
「○○を完了」等の完了依頼のとき)
1. 収集(登録): 「タスク: ○○」のように依頼されたら、○○の部分を title として task_add を呼ぶ。続けて
   task_action_push を action="add"・result にtask_addが返したstatusをそのまま・titleにそのタスクの
   titleを渡して1回呼ぶ(登録直後はまだ整理されていないinbox状態であることが結果に反映される)。
2. 整理・分類: 依頼文やその後のやり取りで次アクション・コンテキスト(@home, @pc等)・期限・プロジェクトが
   分かったら、対象タスクのidに対して task_update で status(next/waiting/someday/done のいずれか)・
   context・due(RFC3339)・project を更新し、続けて task_action_push を action="update"・result に
   task_updateが返したstatusをそのまま・わかっている範囲でtitle/taskStatus/context/due/projectを渡して
   呼ぶ。何が分かっていて何が未定かに応じては、ツールを呼ばず「次のアクションは？」「2分で終わりますか？」
   等を日本語のあなたの最終応答テキストで問い返して構わない。
3. 見直し・一覧: 「タスク一覧」「未処理のタスクは？」等には task_list を(絞り込み条件があれば
   status/context/project を指定して)呼び、続けて task_list_push を heading="タスク一覧"(絞り込み条件が
   あればそれが分かる見出しでもよい)・tasks に task_list が返した各タスクの id・title・status・context・
   due・project をそのまま渡して1回呼ぶ(日時の書式変換は不要)。特に inbox に長く残っているタスクが
   あれば note にその旨を書く。
4. 実行: 「今やるべきタスクは？」等には task_list を status="next" で呼び、続けて task_list_push を
   heading="次にやるべきタスク"・tasks に(context や due が近いものを優先した)上位数件を渡して呼ぶ。
5. 完了: 「○○を完了/終わった」等には、対象タスクの id を(直前のやり取りやtask_listの結果から)特定し
   task_complete を呼び、続けて task_action_push を action="complete"・result にtask_completeが返した
   statusをそのまま渡して呼ぶ。idが特定できない場合は task_list を呼んで task_list_push
   (heading="完了候補")で候補を示し、日本語のあなたの最終応答テキストでどれか尋ねる。

# 業務E: 予定の確認・通知(「今日の予定は？」等の照会、または「今日の予定一覧をSlackに通知して」等の
朝のダイジェスト依頼のとき。読み取り専用の業務のため動作モードに関わらず常に実行してよい)
1. calendar_list_events を呼ぶ。期間の指定がなければ timeMinRFC3339/timeMaxRFC3339 は省略してよい
   (省略時は本日(JST)の予定になる)。指定があれば(「明日」「来週」等)対応する範囲を渡す。
2. calendar_digest_push を1回だけ呼び、取得した予定をそのまま events 引数に渡す(title は各予定の
   summary、when は表示用の日時、htmlLinkは戻り値のものを使う)。予定が0件でもそのまま呼び出すこと
   (「本日の予定はありません」として届く)。

# 注意
- 業務A・業務Dの現在の動作モード: %s。dry_run の場合、ツールは実際の変更を行わずログのみ返すが、手順は同じように実行すること。
- ツールがエラーを返しても、対応するpushツール(業務A: slack_push、業務B: goblog_summary_push、
  業務C: goblog_list_push、業務D: task_action_push/task_list_push、業務E: calendar_digest_push)は
  同じように呼び、note引数にエラー内容を1〜2行で書くこと。ただし goblog_fetch_post・goblog_list_posts
  自体がエラーを返した場合(業務B・Cの前提データが取得できない場合)は該当のpushツールを呼ばず、日本語の
  あなたの最終応答テキストで簡潔に伝える。無限ループを避け、各メール・各記事・各タスク・各予定確認の
  処理は一度だけにする。
- 上記のpushツールを使わず、あなたの最終応答テキストをそのままSlackスレッドに投稿する場合(業務B・Cの
  取得失敗、業務Dの問い返しや完了候補の確認、どの業務にも当てはまらない依頼への返答など)は、Slackの
  Markdown(mrkdwn)は一般的なMarkdown(CommonMark)と記法が異なる点に注意する: 太字は **太字** ではなく
  *太字*(アスタリスク1つ)、リンクは [表示テキスト](URL) ではなく <URL|表示テキスト> の形式で書く。
- どの業務にも当てはまらない依頼の場合は、対応できない旨を日本語で伝える。`

// Config carries the dependencies needed to build the agent.
type Config struct {
	Model     model.LLM
	Clients   *googleapi.Clients
	App       *config.Config
	TaskStore store.TaskStore
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
	ttools, err := tasktools.Tools(cfg.TaskStore, mode)
	if err != nil {
		return nil, fmt.Errorf("task tools: %w", err)
	}

	allTools := make([]tool.Tool, 0, len(gtools)+len(ctools)+len(stools)+len(btools)+len(ttools))
	allTools = append(allTools, gtools...)
	allTools = append(allTools, ctools...)
	allTools = append(allTools, stools...)
	allTools = append(allTools, btools...)
	allTools = append(allTools, ttools...)

	instruction := fmt.Sprintf(instructionTmpl, cfg.App.GmailQuery, mode)

	return llmagent.New(llmagent.Config{
		Name:        cfg.App.AppName,
		Model:       cfg.Model,
		Description: "受信メールを整理(分類・ラベル付け)し、予定をカレンダー登録し、要約をSlack通知する秘書エージェント。Go blogのURLを渡すと要約・翻訳も行い、URLがなくても最近の記事一覧・要約に対応する。GTDタスクの登録・整理・一覧・完了も行う。カレンダーの予定確認・朝のダイジェスト通知にも対応する。",
		Instruction: instruction,
		Tools:       allTools,
	})
}
