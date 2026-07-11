# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A personal secretary agent built with ADK for Go (`google.golang.org/adk`) + Gemini. The first feature is a Gmail triage agent: it classifies incoming mail into "needs review / unwanted / has schedule", labels unwanted mail, registers events in Google Calendar, and sends a summary via a Slack Bot (`chat.postMessage`), with needs-review mail linked to Gmail and calendar registrations linked to the event. The same agent also fetches, summarizes, and translates Go blog (`go.dev/blog/...`) posts on request — typically invoked via the Slack `@mention` listener. README.md (Japanese) has the full env-var table and setup walkthrough.

## Commands

```sh
go build ./...                       # build everything
go vet ./...                        # lint (no other linter configured)
go test ./...                       # tests (currently just internal/tools/goblog's HTML-extraction logic)

ACTION_MODE=dry_run go run ./cmd/batch   # one-shot agent run, no real changes
go run ./cmd/api web api webui           # dev server with Web UI at :8080
go run ./cmd/oauth                       # one-time local helper to obtain a refresh token
go run ./cmd/migrate -command up         # create/update MySQL session tables (needs MYSQL_DSN)
```

Required env for the agent to actually run: `GOOGLE_API_KEY`, `GOOGLE_OAUTH_CLIENT_ID/_SECRET/_REFRESH_TOKEN` (see README). Without `MYSQL_DSN` sessions are in-memory, which is fine locally. If `SLACK_BOT_TOKEN`/`SLACK_APP_TOKEN` are set, `cmd/api` also starts a Slack Socket Mode listener (`internal/slackbot`) so the agent can be invoked by `@mention`ing the bot; otherwise it's skipped silently. The notify tool posts the summary via the same `SLACK_BOT_TOKEN` to `SLACK_CHANNEL_ID`; if either is unset, notification is skipped (not an error).

**ADK launcher gotcha**: the `web` launcher requires its sublaunchers listed explicitly as args (`api`, `a2a`, `webui`). Omitting them fails with `no active sublaunchers found`. Prod runs headless via `ADK_LAUNCHER=prod ./api web api a2a` (the Dockerfile bakes this in as ENTRYPOINT/CMD).

## Architecture

Four entry points in `cmd/` share one dependency builder, `internal/app.Build()`, which assembles: Gemini model → Google OAuth clients (`internal/google`) → the gmail agent → session service. Anything wired into both the API server and the batch belongs there.

- `cmd/api` — always-on ADK REST API server (`POST /run`, `/run_sse`) for a k8s Deployment. Also owns the optional Slack Socket Mode listener (`internal/slackbot`), started as a goroutine alongside the ADK launcher — not wired into `internal/app.Build()` since only the API server needs it.
- `cmd/batch` — one-shot `runner.Run()` invocation, triggered by an ArgoWorkflows CronWorkflow. Creates its own session (`cron-<timestamp>`) before running, because `runner.Run` requires an existing session.
- `cmd/migrate` — schema migration, run as an ArgoCD PreSync Job before pods roll out.
- `cmd/oauth` — local-only refresh-token helper.

The agent (`internal/agents/gmail`) is an `llmagent` whose behavior is driven entirely by its instruction prompt (in Japanese — user-facing output is Japanese) plus function tools from `internal/tools/{gmail,calendar,notify,goblog}`. The instruction routes between two tasks based on what the caller asked for: the Gmail triage flow, or (given a `go.dev/blog/...` URL) fetching, summarizing, and translating a Go blog post via `goblog_fetch_post` — the tool only returns raw title/text; the LLM does the summarizing/translating itself. Safety is enforced at the tool layer, not just in the prompt — see `.claude/rules/action-mode-safety.md`. `goblog_fetch_post` is read-only and hard-restricted to the `go.dev` host to avoid becoming an open URL fetcher.

`internal/agents/llmauditor.go` and `image_generator.go` are standalone sample agents not wired into the app.

## Rules

Situational invariants live in `.claude/rules/`:

- `action-mode-safety.md` — ACTION_MODE gating; applies when adding or changing agent tools.
- `mysql-sessions.md` — timestamp-precision invariants in `internal/store`; applies when touching the store or upgrading the ADK module.
- `ci-cd-contract.md` — the cross-repo GitOps contract; applies when touching the workflow or Dockerfile.
