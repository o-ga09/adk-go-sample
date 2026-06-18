// Command batch runs the Gmail secretary agent once and exits. It is intended
// to be invoked on a schedule by an ArgoWorkflows CronWorkflow (one Job per
// run). All behaviour is driven by environment configuration.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/o-ga09/adk-go-sample/internal/app"
	"github.com/o-ga09/adk-go-sample/internal/config"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("batch failed: %v", err)
	}
}

func run() error {
	ctx := context.Background()
	c := config.Load()
	if err := c.ValidateForBatch(); err != nil {
		return err
	}
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
			}
		}
	}

	log.Printf("batch complete. final: %s", lastText)
	_ = os.Stdout.Sync()
	return nil
}
