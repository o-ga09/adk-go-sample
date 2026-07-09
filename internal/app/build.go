// Package app wires together the shared dependencies (model, Google clients,
// session service, agent) used by both the API server and the cron batch.
package app

import (
	"context"
	"fmt"

	gmailagent "github.com/o-ga09/adk-go-sample/internal/agents/gmail"
	"github.com/o-ga09/adk-go-sample/internal/config"
	googleapi "github.com/o-ga09/adk-go-sample/internal/google"
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
