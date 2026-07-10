# インフラリポジトリ作業指示書（Claude 向け）

これは **別リポジトリ(インフラ/GitOps リポジトリ)で作業する Claude** に渡すための指示書です。
アプリ本体は `o-ga09/adk-go-sample`（ADK for Go + Gemini の秘書エージェント）。
このリポジトリの GitHub Actions が **GAR へイメージを push し、本インフラリポの manifest 内の image tag を `yq` で書き換えて main へ直 push** します。

あなた(infra リポの Claude)のゴールは、**おうち k8s 上でこのアプリを動かす manifest 一式を作成すること**です。

---

## 1. 動かすワークロード

| ワークロード | 種別 | 役割 | 起動コマンド |
|---|---|---|---|
| `secretary-api` | Deployment + Service | 常時起動の API/UI サーバ。ADK REST API (`POST /run`, `/run_sse`) を提供。`SLACK_BOT_TOKEN`/`SLACK_APP_TOKEN` を設定すると同一プロセス内でSlack Socket Modeリスナー(`@メンション`でGmail整理・Go blog要約翻訳エージェントを呼び出し)もgoroutineとして起動。 | `/app/api`（`ADK_LAUNCHER=prod`）|
| `secretary-gmail-batch` | Argo **CronWorkflow** | Gmail 整理バッチ。1回実行して終了。 | `/app/batch` |

両方とも**同じイメージ**を使う（イメージに `api`/`batch`/`oauth` の3バイナリが同梱されている）。
コンテナの listen ポートは **8080**。

> Slack Socket Modeはアプリ側からSlackへの **outbound WebSocket接続のみ**で完結する。新規Deployment/Serviceやinbound ingress/公開エンドポイントは不要。おうちk8sのegressがデフォルト許可であれば追加設定不要だが、egressを制限している場合は `secretary-api` から Slack (443/wss) への outbound を許可すること。
>
> Go blog要約・翻訳ツール(`goblog_fetch_post`)も同じ `secretary-api` プロセス内で動く読み取り専用ツールで、新規env var・Secret・ワークロードは不要(`https://go.dev/...` への outbound HTTPS のみ使用。egress制限時は `go.dev` への許可も必要)。

---

## 2. ⚠️ CI が依存する「壊してはいけない契約」

アプリ側 CI（`.github/workflows/build-and-deploy.yml`）は以下の `yq` 式で image を書き換えます。
**この構造（キー名・配列要素名）を変えると CI のイメージ更新が壊れます。**

- **API Deployment**: container 配列の中で **`name: api`** の要素の **`.image`** を更新
  ```
  (.spec.template.spec.containers[] | select(.name == "api") | .image)
  ```
- **Argo CronWorkflow**: templates 配列の中で **`name: run-batch`** の要素の **`.container.image`** を更新
  ```
  (.spec.workflowSpec.templates[] | select(.name == "run-batch") | .container.image)
  ```

したがって作成する manifest では必ず:
- API Deployment のコンテナ名を **`api`** にする
- CronWorkflow のテンプレート名を **`run-batch`** にし、コンテナを **`container:`**（`script:` ではない）で書く

---

## 3. 作成するファイル

以下のレイアウトを推奨（パスは自由だが、決めたら §6 のリポジトリ Variables に登録する）。

```
apps/secretary/
  namespace.yaml
  api-deployment.yaml      # ← INFRA_API_MANIFEST に指定
  cronworkflow.yaml        # ← INFRA_CRON_MANIFEST に指定
  secret.md                # Secret の作成手順（実値はコミットしない）
```

`image:` の初期値は `<GAR_LOCATION>-docker.pkg.dev/<GCP_PROJECT>/<GAR_REPOSITORY>/<IMAGE_NAME>:latest`
の形にしておく（CI が初回以降 SHA タグに書き換える）。実プロジェクト値で埋めること。

### 3.1 namespace.yaml
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: secretary
```

### 3.2 api-deployment.yaml
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: secretary-api
  namespace: secretary
  labels: { app: secretary-api }
spec:
  replicas: 1
  selector:
    matchLabels: { app: secretary-api }
  template:
    metadata:
      labels: { app: secretary-api }
    spec:
      containers:
        - name: api                       # ← CI が参照。変更禁止
          image: REGION-docker.pkg.dev/PROJECT/REPO/adk-go-sample:latest
          command: ["/app/api"]
          env:
            - name: ADK_LAUNCHER
              value: "prod"               # headless API + A2A（Web UI なし）
            - name: APP_NAME
              value: "gmail_secretary"
          envFrom:
            - secretRef:
                name: secretary-secrets   # §4 参照
          ports:
            - containerPort: 8080
          readinessProbe:
            httpGet: { path: /, port: 8080 }
            initialDelaySeconds: 5
            periodSeconds: 10
          resources:
            requests: { cpu: "50m", memory: "128Mi" }
            limits:   { memory: "512Mi" }
---
apiVersion: v1
kind: Service
metadata:
  name: secretary-api
  namespace: secretary
spec:
  selector: { app: secretary-api }
  ports:
    - port: 80
      targetPort: 8080
```
> UI を外部公開する場合は別途 Ingress/Gateway を追加。おうち k8s の既存 Ingress 構成に合わせること。

### 3.3 cronworkflow.yaml（Argo Workflows）
```yaml
apiVersion: argoproj.io/v1alpha1
kind: CronWorkflow
metadata:
  name: secretary-gmail-batch
  namespace: secretary
spec:
  schedule: "0 8 * * *"          # 毎日 08:00
  timezone: "Asia/Tokyo"
  concurrencyPolicy: "Forbid"
  startingDeadlineSeconds: 300
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 3
  workflowSpec:
    entrypoint: run-batch
    activeDeadlineSeconds: 600
    templates:
      - name: run-batch            # ← CI が参照。変更禁止
        retryStrategy:
          limit: "2"
          retryPolicy: "OnFailure"
        container:                 # ← script: ではなく container: で書く
          image: REGION-docker.pkg.dev/PROJECT/REPO/adk-go-sample:latest
          command: ["/app/batch"]
          env:
            - name: APP_NAME
              value: "gmail_secretary"
            - name: ACTION_MODE
              value: "label_only"   # 初回検証は dry_run 推奨。順に label_only → (任意)auto_trash
            - name: GMAIL_QUERY
              value: "in:inbox is:unread newer_than:1d"
          envFrom:
            - secretRef:
                name: secretary-secrets
```
> Argo Workflows の controller が namespace `secretary` を対象にしているか確認。
> ServiceAccount を必要とする構成なら `serviceAccountName` を `workflowSpec` に追加すること。

---

## 4. Secret（実値はコミットしない）

アプリは以下を **環境変数**として要求する（`secretary-secrets` を `envFrom` で注入）。

| キー | 内容 |
|---|---|
| `GOOGLE_API_KEY` | Gemini API キー |
| `GOOGLE_OAUTH_CLIENT_ID` / `GOOGLE_OAUTH_CLIENT_SECRET` | Google OAuth クライアント |
| `GOOGLE_OAUTH_REFRESH_TOKEN` | 個人 Gmail の refresh token（アプリ repo の `cmd/oauth` で取得） |
| `MYSQL_DSN` | 例 `user:pass@tcp(mysql.secretary.svc:3306)/secretary?parseTime=true` |
| `SLACK_WEBHOOK_URL` | Slack Incoming Webhook URL(バッチ結果通知の既定チャネル) |
| `SLACK_BOT_TOKEN` | Slack Bot Token(`xoxb-...`)。未設定なら Slack `@メンション`リスナーは起動しない |
| `SLACK_APP_TOKEN` | Slack App-Level Token(`xapp-...`)。Socket Mode接続用。未設定なら同上 |
| `SLACK_ALLOWED_USER_ID` | この Slack ユーザーID以外の`@メンション`を拒否(**強く推奨**。Gmail/Calendar書き込み権限を持つエージェントを誰でも呼び出せてしまうため) |
| `LINE_CHANNEL_TOKEN` / `LINE_TARGET_USER_ID` | LINE Messaging API(フォールバック通知) |

> `SLACK_*` / `LINE_*` はいずれも未設定で構わない(該当機能がスキップされるだけでアプリ自体は起動する)。Go blog要約・翻訳ツールが使う env var は無い。

**作成方法（このリポの方針に合わせて1つ選ぶ）:**
- このクラスタに **SealedSecrets / ExternalSecrets** が入っているなら、それを使って暗号化した Secret をコミットする（推奨。平文をコミットしない）。
- 無ければ手動作成（コミットしない）:
  ```sh
  kubectl -n secretary create secret generic secretary-secrets \
    --from-literal=GOOGLE_API_KEY=... \
    --from-literal=GOOGLE_OAUTH_CLIENT_ID=... \
    --from-literal=GOOGLE_OAUTH_CLIENT_SECRET=... \
    --from-literal=GOOGLE_OAUTH_REFRESH_TOKEN=... \
    --from-literal=MYSQL_DSN='user:pass@tcp(mysql.secretary.svc:3306)/secretary?parseTime=true' \
    --from-literal=SLACK_WEBHOOK_URL=... \
    --from-literal=SLACK_BOT_TOKEN=... \
    --from-literal=SLACK_APP_TOKEN=... \
    --from-literal=SLACK_ALLOWED_USER_ID=... \
    --from-literal=LINE_CHANNEL_TOKEN=... \
    --from-literal=LINE_TARGET_USER_ID=...
  ```
`secret.md` にはこの手順だけ書き、**実値は絶対に commit しない**こと。

---

## 5. GAR からの image pull 設定

ノード/Pod が GAR からイメージを pull できる必要がある。クラスタの方式に合わせていずれかを設定:
- ノードに GAR 読み取り権限がある（GKE 等）なら追加設定不要。
- おうち k8s（GKE 外）の場合は **GAR 読み取り用 SA キーで `imagePullSecrets`** を作成し、Deployment と CronWorkflow の Pod spec に付与する。
  ```sh
  kubectl -n secretary create secret docker-registry gar-pull \
    --docker-server=REGION-docker.pkg.dev \
    --docker-username=_json_key \
    --docker-password="$(cat gar-reader-key.json)" \
    --docker-email=unused@example.com
  ```
  → Deployment は `spec.template.spec.imagePullSecrets`、CronWorkflow は `workflowSpec.imagePullSecrets` に `- name: gar-pull` を追加。

---

## 6. アプリ側 CI に登録してもらう値（このリポの担当者へ連携）

manifest のパスを決めたら、**アプリリポ `o-ga09/adk-go-sample`** の
Settings > Secrets and variables > Actions に以下を登録する必要がある（あなたの作業ではないが、整合のため明記）:

- Variables: `INFRA_REPO`(このインフラリポ), `INFRA_API_MANIFEST`(例 `apps/secretary/api-deployment.yaml`), `INFRA_CRON_MANIFEST`(例 `apps/secretary/cronworkflow.yaml`), `GAR_LOCATION`, `GCP_PROJECT`, `GAR_REPOSITORY`, `IMAGE_NAME`
- Secrets: `GCP_WIF_PROVIDER`, `GCP_SA_EMAIL`, `INFRA_REPO_TOKEN`(このインフラリポへ push 可能な PAT)

---

## 7. （任意）ArgoCD で同期する場合

GitOps を ArgoCD で回すなら Application を1つ追加:
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: secretary
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/<OWNER>/<THIS_INFRA_REPO>.git
    targetRevision: main
    path: apps/secretary
  destination:
    server: https://kubernetes.default.svc
    namespace: secretary
  syncPolicy:
    automated: { prune: true, selfHeal: true }
    syncOptions: [CreateNamespace=true]
```

---

## 8. 完了チェックリスト

- [ ] `apps/secretary/` に namespace / api-deployment / cronworkflow を作成（image は実 GAR パス）
- [ ] API Deployment の container 名が **`api`**、CronWorkflow の template 名が **`run-batch`**（CI 契約）
- [ ] `secretary-secrets` を SealedSecrets/ExternalSecrets もしくは手動で用意（平文は未コミット。Slackを使うなら `SLACK_BOT_TOKEN`/`SLACK_APP_TOKEN`/`SLACK_ALLOWED_USER_ID`/`SLACK_WEBHOOK_URL` も含める）
- [ ] GAR pull 設定（imagePullSecrets もしくはノード権限）
- [ ] Argo Workflows controller が `secretary` namespace を対象にしている
- [ ] egressを制限しているクラスタなら `secretary-api` から Slack (`*.slack.com` 443/wss) と `go.dev` (443) への outbound を許可
- [ ] （ArgoCD 運用なら）Application 追加
- [ ] 動作確認: `kubectl -n secretary get deploy,svc`、`argo -n secretary submit --from cronwf/secretary-gmail-batch` で Job 成功
- [ ] アプリ repo 側に §6 の Variables/Secrets が登録されていることを担当者に確認
