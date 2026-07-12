// Command batch runs the Gmail secretary agent once and exits. It is
// intended to be invoked on a schedule by an ArgoWorkflows CronWorkflow (one
// Job per run). All behaviour is driven by environment configuration.
//
// It supports three -command values:
//
//	batch -command mail              # default: triage the inbox (existing CronWorkflow behaviour)
//	batch -command llm-cost-report   # post yesterday's LLM usage cost summary to Slack
//	batch -command weekly-review     # post the GTD weekly-review summary to Slack
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/o-ga09/adk-go-sample/internal/app"
	"github.com/o-ga09/adk-go-sample/internal/config"
	"github.com/o-ga09/adk-go-sample/internal/llmusage"
	"github.com/o-ga09/adk-go-sample/internal/store"
	"github.com/o-ga09/adk-go-sample/internal/weeklyreview"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func main() {
	command := flag.String("command", "mail", "batch command to run: mail (default), llm-cost-report, or weekly-review")
	flag.Parse()

	var err error
	switch *command {
	case "mail":
		err = runMail()
	case "llm-cost-report":
		err = runLLMCostReport()
	case "weekly-review":
		err = runWeeklyReview()
	default:
		err = fmt.Errorf("unsupported -command %q (want mail|llm-cost-report|weekly-review)", *command)
	}
	if err != nil {
		log.Fatalf("batch failed: %v", err)
	}
}

// runMail triages the inbox: the batch's original (and only, pre-#16)
// behaviour. Kept as the default -command so the existing CronWorkflow,
// which invokes the binary with no arguments, is unaffected.
func runMail() error {
	ctx := context.Background()
	c := config.Load()
	if err := c.ValidateForBatch(); err != nil {
		return err
	}
	ctx = llmusage.WithTrigger(ctx, llmusage.TriggerBatch)
	log.Printf("starting gmail batch: mode=%s query=%q", c.ActionMode, c.GmailQuery)

	deps, err := app.Build(ctx, c)
	if err != nil {
		return err
	}

	r, err := runner.New(runner.Config{
		AppName:         c.AppName,
		Agent:           deps.Agent,
		SessionService:  deps.SessionService,
		ArtifactService: deps.ArtifactService,
	})
	if err != nil {
		return fmt.Errorf("create runner: %w", err)
	}

	const userID = "owner"
	sessionID := "cron-" + time.Now().Format("20060102-150405")

	// runner.Run requires the session to already exist.
	if _, err := deps.SessionService.Create(ctx, &session.CreateRequest{
		AppName:   c.AppName,
		UserID:    userID,
		SessionID: sessionID,
	}); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	msg := genai.NewContentFromText("受信トレイを整理して通知してください。", genai.RoleUser)

	var lastText string
	for ev, err := range r.Run(ctx, userID, sessionID, msg, agent.RunConfig{}) {
		if err != nil {
			return fmt.Errorf("agent run: %w", err)
		}
		if ev != nil && ev.Content != nil {
			for _, p := range ev.Content.Parts {
				if p.Text != "" {
					lastText = p.Text
					log.Printf("[%s] %s", ev.Author, p.Text)
				}
				if p.FunctionCall != nil {
					log.Printf("[%s] tool-call: %s", ev.Author, p.FunctionCall.Name)
				}
				if p.FunctionResponse != nil {
					log.Printf("[%s] tool-response: %s %s", ev.Author, p.FunctionResponse.Name, compactJSON(p.FunctionResponse.Response))
				}
			}
		}
	}

	log.Printf("batch complete. final: %s", lastText)
	_ = os.Stdout.Sync()
	return nil
}

// runLLMCostReport aggregates yesterday's (JST) LLM token usage recorded by
// internal/llmusage and posts a cost summary to Slack. It intentionally does
// not call app.Build: this command never talks to Gmail/Calendar/Gemini, so
// it skips the Google OAuth setup app.Build would otherwise require.
func runLLMCostReport() error {
	ctx := context.Background()
	c := config.Load()

	if c.MySQLDSN == "" {
		// Nothing was recorded (see store.UsageRecorder's no-op mode), so
		// there is nothing to report; this must not be treated as failure.
		log.Print("llm cost report skipped: MYSQL_DSN not set")
		return nil
	}

	st, err := store.NewUsageRecorder(c)
	if err != nil {
		return fmt.Errorf("open usage store: %w", err)
	}

	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		loc = time.UTC
	}
	yesterday := time.Now().In(loc).AddDate(0, 0, -1)

	report, err := llmusage.BuildDailyReport(ctx, st, yesterday, c.LLMCostDailyAlertUSD)
	if err != nil {
		return fmt.Errorf("build daily report: %w", err)
	}

	blocks := llmusage.FormatSlackMessage(report)
	log.Printf("llm cost report: date=%s cost=$%.4f requests=%d tokens=%d exceeded=%v",
		report.Date, report.TotalCostUSD(), report.TotalRequests(), report.TotalTokens(), report.Exceeded())

	if c.SlackBotToken == "" || c.SlackChannelID == "" {
		log.Print("llm cost report: Slack not configured, skipping post")
		return nil
	}
	if err := llmusage.PostToSlack(ctx, c.SlackBotToken, c.SlackChannelID, blocks, report.Exceeded()); err != nil {
		return fmt.Errorf("post to slack: %w", err)
	}
	return nil
}

// runWeeklyReview aggregates the current GTD task state (internal/store's
// TaskStore) and posts a weekly-review summary to Slack: unprocessed inbox
// items, stalled next/waiting tasks, and prioritized next actions. Like
// runLLMCostReport it does not call app.Build: this command never talks to
// Gmail/Calendar/Gemini, so it skips the Google OAuth setup app.Build would
// otherwise require.
func runWeeklyReview() error {
	ctx := context.Background()
	c := config.Load()

	if c.MySQLDSN == "" {
		// The in-memory TaskStore fallback starts empty on every process, so
		// there is nothing meaningful to review; this must not be treated as
		// failure (mirrors runLLMCostReport's MYSQL_DSN-unset handling).
		log.Print("weekly review skipped: MYSQL_DSN not set")
		return nil
	}

	st, err := store.NewTaskStore(c.MySQLDSN)
	if err != nil {
		return fmt.Errorf("open task store: %w", err)
	}

	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)

	report, err := weeklyreview.BuildReport(ctx, st, now, c.WeeklyReviewStaleDays)
	if err != nil {
		return fmt.Errorf("build weekly review: %w", err)
	}

	text := weeklyreview.FormatSlackMessage(report)
	log.Print(text)

	if c.SlackBotToken == "" || c.SlackChannelID == "" {
		log.Print("weekly review: Slack not configured, skipping post")
		return nil
	}
	if err := weeklyreview.PostToSlack(ctx, c.SlackBotToken, c.SlackChannelID, text); err != nil {
		return fmt.Errorf("post to slack: %w", err)
	}
	return nil
}

// compactJSON renders a tool's response map for the pod log so failures
// surface with their real API error, truncated so a large mail body cannot
// flood the log. When the ADK itself fails a tool call it puts a raw error
// value in the map, which json.Marshal would render as {}; stringify those
// first (on a copy — the map is the live event payload).
func compactJSON(v map[string]any) string {
	m := make(map[string]any, len(v))
	for k, val := range v {
		if e, ok := val.(error); ok {
			m[k] = e.Error()
		} else {
			m[k] = val
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	const max = 2000
	if len(b) > max {
		return string(b[:max]) + "...(truncated)"
	}
	return string(b)
}
