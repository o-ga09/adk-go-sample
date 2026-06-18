// Package google builds authenticated Gmail and Calendar API clients from an
// OAuth2 refresh token. A personal Gmail account cannot use a service account,
// so we use the installed-app refresh-token flow (see cmd/oauth).
package google

import (
	"context"

	"github.com/o-ga09/adk-go-sample/internal/config"
	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
	calendar "google.golang.org/api/calendar/v3"
	gmail "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// Scopes requested for the secretary agent.
//   - gmail.modify: read, label, and trash mail (NOT permanent delete -> safer)
//   - calendar.events: create/read calendar events on the primary calendar
var Scopes = []string{
	gmail.GmailModifyScope,
	calendar.CalendarEventsScope,
}

// OAuthConfig returns the oauth2.Config used for both the auth helper and the
// runtime token source.
func OAuthConfig(c *config.Config) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.OAuthClientID,
		ClientSecret: c.OAuthClientSecret,
		Endpoint:     googleoauth.Endpoint,
		Scopes:       Scopes,
		RedirectURL:  "http://localhost:8080/callback",
	}
}

// TokenSource builds a refreshing token source from the stored refresh token.
func TokenSource(ctx context.Context, c *config.Config) oauth2.TokenSource {
	cfg := OAuthConfig(c)
	tok := &oauth2.Token{RefreshToken: c.OAuthRefreshToken}
	return cfg.TokenSource(ctx, tok)
}

// Clients bundles the Google API services the tools depend on.
type Clients struct {
	Gmail    *gmail.Service
	Calendar *calendar.Service
}

// NewClients constructs authenticated Gmail and Calendar services.
func NewClients(ctx context.Context, c *config.Config) (*Clients, error) {
	ts := TokenSource(ctx, c)

	gsvc, err := gmail.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	csvc, err := calendar.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	return &Clients{Gmail: gsvc, Calendar: csvc}, nil
}
