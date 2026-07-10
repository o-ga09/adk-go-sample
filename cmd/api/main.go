// Command api runs the always-on ADK server that backs the UI. It exposes the
// Gmail secretary agent over the ADK REST API (POST /run, /run_sse). If
// SLACK_BOT_TOKEN/SLACK_APP_TOKEN are set, it also starts a Slack Socket Mode
// listener so the agent can be invoked by @mentioning the bot.
//
// By default it uses the "full" launcher (console + Web UI + API), which is
// convenient for local development. Set ADK_LAUNCHER=prod for a headless
// API+A2A server suitable for k8s.
package main

import (
	"context"
	"log"
	"os"

	"github.com/o-ga09/adk-go-sample/internal/app"
	"github.com/o-ga09/adk-go-sample/internal/config"
	"github.com/o-ga09/adk-go-sample/internal/slackbot"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/adk"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/cmd/launcher/prod"
	"google.golang.org/adk/server/restapi/services"
)

func main() {
	ctx := context.Background()
	c := config.Load()

	deps, err := app.Build(ctx, c)
	if err != nil {
		log.Fatalf("failed to build app: %v", err)
	}

	agentLoader, err := services.NewMultiAgentLoader(deps.Agent)
	if err != nil {
		log.Fatalf("failed to create agent loader: %v", err)
	}

	if c.SlackBotToken != "" && c.SlackAppToken != "" {
		bot, err := slackbot.New(slackbot.Config{
			App:             c,
			Agent:           deps.Agent,
			SessionService:  deps.SessionService,
			ArtifactService: deps.ArtifactService,
		})
		if err != nil {
			log.Fatalf("failed to create slack bot: %v", err)
		}
		go func() {
			if err := bot.Run(ctx); err != nil {
				log.Printf("slack bot stopped: %v", err)
			}
		}()
	} else {
		log.Print("slack bot disabled: SLACK_BOT_TOKEN/SLACK_APP_TOKEN not set")
	}

	cfg := &adk.Config{
		ArtifactService: deps.ArtifactService,
		SessionService:  deps.SessionService,
		AgentLoader:     agentLoader,
	}

	var l launcher.Launcher
	if os.Getenv("ADK_LAUNCHER") == "prod" {
		l = prod.NewLaucher() // API + A2A, no Web UI (headless)
	} else {
		l = full.NewLauncher() // console + Web UI + API (dev)
	}

	if err := l.Execute(ctx, cfg, os.Args[1:]); err != nil {
		log.Fatalf("run failed: %v\n\n%s", err, l.CommandLineSyntax())
	}
}
