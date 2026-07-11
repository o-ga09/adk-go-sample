# adk-go-sample — 自分専用秘書エージェント

ADK for Go + Gemini で作る、自分専用の秘書エージェント。第一弾は **Gmail 整理エージェント**。

- 受信メールを「要確認 / 不要 / 予定あり」に分類
- 不要メールに Gmail ラベルを付与(※当面は**削除しない**)
- 予定は Google カレンダーへ登録
- 結果の要約を **Slack Bot**(`chat.postMessage`)で通知
- Slack で bot に **@メンション**すると、その場でエージェントを呼び出せる(Socket Mode)
- Slack で `go.dev/blog/...` の URL を渡すと、その **Go blog 記事を要約・翻訳**して返信
- 定期実行は **ArgoWorkflows CronWorkflow**、UI は **ADK REST API**(`POST /run`, `/run_sse`)で接続

## 構成

| パス | 役割 |
|---|---|
| `cmd/api` | 常時起動の API/UI サーバ(k8s Deployment)。`ADK_LAUNCHER=prod` で headless。 |
| `cmd/batch` | cron 用ワンショット。`runner.Run()` を1回実行して終了(Argo Job)。 |
| `cmd/oauth` | 個人 Gmail の OAuth refresh token を取得するローカル用ヘルパ。 |
| `internal/agents/gmail` | 秘書 LLM エージェント定義(Gmail 整理 / Go blog 要約・翻訳)。 |
| `internal/tools/{gmail,calendar,notify,goblog}` | ADK function tools(Gmail / Calendar / Slack / Go blog 取得)。 |
| `internal/slackbot` | Slack Socket Mode リスナー(`@メンション`→エージェント実行→スレッド返信)。`cmd/api` 内で起動。 |
| `internal/google` | OAuth から Gmail/Calendar クライアント生成。 |
| `internal/store` | MySQL バックエンドの session.Service(未設定時は in-memory)。 |
| `internal/app` | 上記を組み立てる共有ビルダー。 |
| `Dockerfile` | api / batch / oauth の3バイナリを同梱したイメージ。 |
| `.github/workflows/build-and-deploy.yml` | ビルド→GAR push→インフラリポの image tag 書き換え。 |

> k8s マニフェスト(Deployment / Argo CronWorkflow / Secret)は**別のインフラリポジトリ**で管理する。本リポジトリはイメージのビルドと push、インフラリポの image tag 更新のみを担う。

## 環境変数

| 変数 | 説明 | 既定 |
|---|---|---|
| `GOOGLE_API_KEY` | Gemini の API キー | (必須) |
| `MODEL_NAME` | Gemini モデル名 | `gemini-2.5-flash` |
| `GOOGLE_OAUTH_CLIENT_ID` / `_SECRET` | OAuth クライアント | (必須) |
| `GOOGLE_OAUTH_REFRESH_TOKEN` | `cmd/oauth` で取得 | (必須) |
| `MYSQL_DSN` | 例 `user:pass@tcp(mysql:3306)/secretary?parseTime=true`。空なら in-memory | "" |
| `SLACK_BOT_TOKEN` | Slack Bot Token(`xoxb-...`)。メンション返信と通知サマリ投稿(`chat.postMessage`)に使用 | "" |
| `SLACK_CHANNEL_ID` | 通知サマリの投稿先チャンネル ID | "" (未設定なら通知スキップ) |
| `SLACK_APP_TOKEN` | Slack App-Level Token(`xapp-...`)。Socket Mode 接続に使用 | "" |
| `SLACK_ALLOWED_USER_ID` | この Slack ユーザー ID からのメンションのみ受け付ける | "" (無指定なら全員可) |
| `GMAIL_QUERY` | 整理対象の Gmail 検索クエリ | `in:inbox is:unread newer_than:1d` |
| `ACTION_MODE` | `dry_run` \| `label_only` \| `auto_trash` | `label_only` |
| `APP_NAME` | ADK アプリ名(UI の appName) | `gmail_secretary` |

## セットアップ手順

1. **OAuth クライアント作成**: Google Cloud Console で「OAuth クライアント ID(デスクトップ/ウェブ)」を作成し、リダイレクト URI に `http://localhost:8080/callback` を追加。Gmail API と Calendar API を有効化。
2. **refresh token 取得**(ローカルで一度だけ):
   ```sh
   GOOGLE_OAUTH_CLIENT_ID=... GOOGLE_OAUTH_CLIENT_SECRET=... go run ./cmd/oauth
   ```
   表示された URL で認可 → 出力された refresh token を保存。
3. **dry-run で動作確認**:
   ```sh
   ACTION_MODE=dry_run go run ./cmd/batch
   ```
   ログで分類結果・付与予定ラベル・カレンダー登録予定・Slack 通知文面を確認(実変更なし)。
4. **本番モード(label_only)**:
   ```sh
   ACTION_MODE=label_only go run ./cmd/batch
   ```
5. **UI/API サーバ(dev, Web UI 付き)**:
   ```sh
   go run ./cmd/api web api webui   # http://localhost:8080
   ```
   ADK の `web` は HTTP サーバ本体で、起動するサブサーバ(`api`=REST API / `a2a` / `webui`=開発用UI)を引数で明示指定する必要がある(指定漏れだと `no active sublaunchers found` で起動失敗)。本番は `ADK_LAUNCHER=prod ./api web api a2a`(webui なし)。
   API 直叩きの例:
   ```sh
   curl -N -XPOST localhost:8080/run_sse -H 'Content-Type: application/json' -d '{
     "appName":"gmail_secretary","userId":"me","sessionId":"s1",
     "newMessage":{"role":"user","parts":[{"text":"受信トレイを整理して通知して"}]}
   }'
   ```
6. **Slack から呼び出す/通知を受け取る(任意)**: Slack App を作成し、Socket Mode を有効化。
   - Bot Token Scopes に `app_mentions:read` / `chat:write` を追加してワークスペースにインストール → `SLACK_BOT_TOKEN`(`xoxb-`)。
   - 「Socket Mode」を ON にして App-Level Token を発行(`connections:write` スコープ)→ `SLACK_APP_TOKEN`(`xapp-`)。
   - Event Subscriptions で `app_mention` イベントを購読(Socket Mode なので Request URL の設定は不要)。
   - `SLACK_ALLOWED_USER_ID` に自分の Slack ユーザー ID を設定しておくと、他のユーザーからのメンションを無視できる(推奨。個人のメール/カレンダーを操作できるため)。
   - 通知サマリを投稿させたいチャンネルに Bot を **`/invite`** しておく(未招待だと `not_in_channel` エラーで投稿に失敗する)。チャンネル ID はチャンネル詳細(チャンネル名クリック)の下部からコピーできる → `SLACK_CHANNEL_ID`。
   - `go run ./cmd/api web api webui`(または prod 起動)で自動的にリスナーが起動する。トークン未設定時は何もせずスキップする。`SLACK_BOT_TOKEN` / `SLACK_CHANNEL_ID` のいずれかが未設定の場合、通知サマリの投稿もスキップされる(エラーにはしない)。
7. **Go blog の要約・翻訳を試す**: Slack の bot に `@bot https://go.dev/blog/slices を要約して` のように話しかける。追加の環境変数は不要(読み取り専用ツール)。API 直叩きでも同様に呼び出せる:
   ```sh
   curl -N -XPOST localhost:8080/run_sse -H 'Content-Type: application/json' -d '{
     "appName":"gmail_secretary","userId":"me","sessionId":"s2",
     "newMessage":{"role":"user","parts":[{"text":"https://go.dev/blog/slices を要約して"}]}
   }'
   ```

## CI/CD(本リポジトリの責務)

`main` への push で `.github/workflows/build-and-deploy.yml` が動作する:

1. コンテナイメージを build し、**Google Artifact Registry (GAR)** へ push(tag = commit SHA 先頭12桁 + `latest`)。
2. **インフラリポジトリ**を checkout し、生 manifest 内の image を `yq` で SHA タグへ書き換え、`main` へ直 push(ArgoCD 等の GitOps が同期する想定)。

必要な GitHub Actions 設定:

| 種別 | キー | 内容 |
|---|---|---|
| Variables | `GAR_LOCATION` | 例 `asia-northeast1` |
| Variables | `GCP_PROJECT` | GCP プロジェクト ID |
| Variables | `GAR_REPOSITORY` | Artifact Registry リポジトリ名 |
| Variables | `IMAGE_NAME` | 例 `adk-go-sample` |
| Variables | `INFRA_REPO` | 例 `o-ga09/home-k8s-infra` |
| Variables | `INFRA_API_MANIFEST` | infra リポ内の API Deployment のパス |
| Variables | `INFRA_CRON_MANIFEST` | infra リポ内の CronWorkflow のパス |
| Secrets | `GCP_WIF_PROVIDER` | Workload Identity Provider リソース名 |
| Secrets | `GCP_SA_EMAIL` | デプロイ用 SA の email |
| Secrets | `INFRA_REPO_TOKEN` | infra リポへ push できる PAT(contents:write) |

> yq の書き換え式は、API Deployment の container 名 `api` / CronWorkflow の template 名 `run-batch` を前提にしている。インフラリポ側の manifest 構造に合わせてワークフローの式を調整すること。
>
> インフラリポ側 manifest の env は `secretary-secrets`(GOOGLE_API_KEY / OAuth 一式 / MYSQL_DSN / SLACK_BOT_TOKEN / SLACK_CHANNEL_ID / SLACK_APP_TOKEN など)を `envFrom` で注入する想定。API は `ADK_LAUNCHER=prod`、batch は `command: ["/app/batch"]` で起動する。
>
> 既存デプロイからの移行時は、Secret に `SLACK_CHANNEL_ID` を追加し、`SLACK_WEBHOOK_URL` / `LINE_CHANNEL_TOKEN` / `LINE_TARGET_USER_ID` を削除すること。

## 安全性メモ

- Gmail スコープは `gmail.modify`(ラベル変更・trash は可能だが**完全削除は不可**)。
- `ACTION_MODE=label_only` では削除は一切行わない。`auto_trash` を有効にして初めて `gmail_trash`(30日復元可のゴミ箱移動)が使える。
- 通知は Slack Bot(`chat.postMessage`)経由。`SLACK_BOT_TOKEN` / `SLACK_CHANNEL_ID` のいずれか未設定時は通知処理をスキップする(エラーにはしない)。cron バッチ / ADK REST API / Slack メンションのどの経路から実行しても、通知は同じ Bot Token 経由に統一される。
- Slack の `@メンション`はメールの分類・ラベル付与・カレンダー登録が可能な同一エージェントを呼び出す(`ACTION_MODE` によるゲーティングは変わらない)。ワークスペース内の誰でもメンションできてしまうため、`SLACK_ALLOWED_USER_ID` で本人以外からの呼び出しを拒否することを強く推奨する。
- `goblog_fetch_post` は読み取り専用で、`https://go.dev/...` 以外のホストは拒否する(任意 URL を取得できる SSRF ツールにしないため)。要約・翻訳自体はこのツールではなく呼び出し元のエージェント(LLM)が行う。
