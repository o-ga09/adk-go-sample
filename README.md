# adk-go-sample — 自分専用秘書エージェント

ADK for Go + Gemini で作る、自分専用の秘書エージェント。第一弾は **Gmail 整理エージェント**。

- 受信メールを「要確認 / 不要 / 予定あり」に分類
- 不要メールに Gmail ラベルを付与(※当面は**削除しない**)
- 予定は Google カレンダーへ登録
- 結果の要約を **LINE** に通知
- 定期実行は **ArgoWorkflows CronWorkflow**、UI は **ADK REST API**(`POST /run`, `/run_sse`)で接続

## 構成

| パス | 役割 |
|---|---|
| `cmd/api` | 常時起動の API/UI サーバ(k8s Deployment)。`ADK_LAUNCHER=prod` で headless。 |
| `cmd/batch` | cron 用ワンショット。`runner.Run()` を1回実行して終了(Argo Job)。 |
| `cmd/oauth` | 個人 Gmail の OAuth refresh token を取得するローカル用ヘルパ。 |
| `internal/agents/gmail` | Gmail 整理 LLM エージェント定義。 |
| `internal/tools/{gmail,calendar,notify}` | ADK function tools(Gmail / Calendar / LINE)。 |
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
| `LINE_CHANNEL_TOKEN` / `LINE_TARGET_USER_ID` | LINE Messaging API | "" |
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
   ログで分類結果・付与予定ラベル・カレンダー登録予定・LINE 文面を確認(実変更なし)。
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
> インフラリポ側 manifest の env は `secretary-secrets`(GOOGLE_API_KEY / OAuth 一式 / MYSQL_DSN / LINE_* など)を `envFrom` で注入する想定。API は `ADK_LAUNCHER=prod`、batch は `command: ["/app/batch"]` で起動する。

## 安全性メモ

- Gmail スコープは `gmail.modify`(ラベル変更・trash は可能だが**完全削除は不可**)。
- `ACTION_MODE=label_only` では削除は一切行わない。`auto_trash` を有効にして初めて `gmail_trash`(30日復元可のゴミ箱移動)が使える。
- 旧 LINE Notify は 2025-03-31 終了のため、LINE Messaging API の push を使用。
