// Package app wires together the shared dependencies (model, Google clients,
// session service, agent) used by both the API server and the cron batch.
package app

import (
	"context"
	"fmt"
	"log"

	gmailagent "github.com/o-ga09/adk-go-sample/internal/agents/gmail"
	"github.com/o-ga09/adk-go-sample/internal/config"
	googleapi "github.com/o-ga09/adk-go-sample/internal/google"
	"github.com/o-ga09/adk-go-sample/internal/llmusage"
	"github.com/o-ga09/adk-go-sample/internal/store"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Deps is the assembled set of runtime dependencies.
type Deps struct {
	Config          *config.Config
	Model           model.LLM
	Clients         *googleapi.Clients
	Agent           agent.Agent
	SessionService  session.Service
	ArtifactService artifact.Service
}

// Build constructs all shared dependencies from configuration.
func Build(ctx context.Context, c *config.Config) (*Deps, error) {
	m, err := gemini.NewModel(ctx, c.ModelName, &genai.ClientConfig{APIKey: c.GoogleAPIKey})
	if err != nil {
		return nil, fmt.Errorf("create model: %w", err)
	}
	m = wrapModelWithUsageTracking(ctx, c, m)

	clients, err := googleapi.NewClients(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("create google clients: %w", err)
	}

	ag, err := gmailagent.New(ctx, gmailagent.Config{Model: m, Clients: clients, App: c})
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	sess, err := store.NewSessionService(c)
	if err != nil {
		return nil, fmt.Errorf("create session service: %w", err)
	}

	return &Deps{
		Config:          c,
		Model:           m,
		Clients:         clients,
		Agent:           ag,
		SessionService:  sess,
		ArtifactService: artifact.InMemoryService(),
	}, nil
}

// wrapModelWithUsageTracking decorates m so every call it makes (from any
// trigger surface: batch, Slack, the ADK REST API, since they all share the
// agent built from m) records its token usage and estimated cost. Unlike
// store.NewSessionService, failures here are logged and degrade to a no-op
// recorder rather than failing Build: the source issue for this feature
// (#16) requires that a failure to record usage never stops the agent
// itself from running, so this is intentionally more permissive than the
// session service it sits next to.
func wrapModelWithUsageTracking(_ context.Context, c *config.Config, m model.LLM) model.LLM {
	rec, err := store.NewUsageRecorder(c)
	if err != nil {
		log.Printf("llmusage: usage recording disabled, continuing without it: %v", err)
		rec = &store.UsageRecorder{}
	}
	pricing, err := llmusage.LoadPricing(c.LLMPricingJSON)
	if err != nil {
		log.Printf("llmusage: invalid LLM_PRICING_JSON, falling back to default pricing: %v", err)
		pricing, _ = llmusage.LoadPricing("")
	}
	return llmusage.WrapModel(m, rec, pricing)
}
