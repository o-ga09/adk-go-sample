// Package config loads runtime configuration from environment variables.
// It is shared by the API server (cmd/api) and the cron batch (cmd/batch).
package config

import (
	"fmt"
	"os"
)

// ActionMode controls how aggressively the Gmail agent mutates the mailbox.
type ActionMode string

const (
	// ModeDryRun classifies and reports but performs no mailbox/calendar writes.
	ModeDryRun ActionMode = "dry_run"
	// ModeLabelOnly applies labels and creates calendar events, but never deletes.
	ModeLabelOnly ActionMode = "label_only"
	// ModeAutoTrash additionally moves clearly-unwanted mail to the trash.
	ModeAutoTrash ActionMode = "auto_trash"
)

// Config holds all runtime settings.
type Config struct {
	// Gemini
	GoogleAPIKey string
	ModelName    string

	// Google OAuth (Gmail + Calendar, personal account refresh-token flow)
	OAuthClientID     string
	OAuthClientSecret string
	OAuthRefreshToken string

	// Persistence
	MySQLDSN string

	// Slack Bot Token (chat.postMessage) notification channel.
	SlackBotToken  string // xoxb-..., used to post replies and notifications
	SlackAppToken  string // xapp-..., used to open the Socket Mode connection
	SlackChannelID string // channel the notification summary is posted to
	// SlackAllowedUserID, if set, restricts who may trigger the agent via
	// @mention to this single Slack user ID. This is a personal secretary
	// agent with mailbox/calendar write access, so anyone else in a channel
	// the bot is added to must not be able to drive it.
	SlackAllowedUserID string

	// Behaviour
	GmailQuery string
	ActionMode ActionMode

	// API server
	AppName string
}

// Load reads configuration from the environment, applying sensible defaults.
func Load() *Config {
	c := &Config{
		GoogleAPIKey:       os.Getenv("GOOGLE_API_KEY"),
		ModelName:          envOr("MODEL_NAME", "gemini-2.5-flash"),
		OAuthClientID:      os.Getenv("GOOGLE_OAUTH_CLIENT_ID"),
		OAuthClientSecret:  os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET"),
		OAuthRefreshToken:  os.Getenv("GOOGLE_OAUTH_REFRESH_TOKEN"),
		MySQLDSN:           os.Getenv("MYSQL_DSN"),
		SlackBotToken:      os.Getenv("SLACK_BOT_TOKEN"),
		SlackAppToken:      os.Getenv("SLACK_APP_TOKEN"),
		SlackChannelID:     os.Getenv("SLACK_CHANNEL_ID"),
		SlackAllowedUserID: os.Getenv("SLACK_ALLOWED_USER_ID"),
		GmailQuery:         envOr("GMAIL_QUERY", "in:inbox is:unread newer_than:1d"),
		ActionMode:         ActionMode(envOr("ACTION_MODE", string(ModeLabelOnly))),
		AppName:            envOr("APP_NAME", "gmail_secretary"),
	}
	return c
}

// ValidateForBatch ensures the settings required by the cron batch are present.
func (c *Config) ValidateForBatch() error {
	missing := []string{}
	if c.GoogleAPIKey == "" {
		missing = append(missing, "GOOGLE_API_KEY")
	}
	if c.OAuthClientID == "" {
		missing = append(missing, "GOOGLE_OAUTH_CLIENT_ID")
	}
	if c.OAuthClientSecret == "" {
		missing = append(missing, "GOOGLE_OAUTH_CLIENT_SECRET")
	}
	if c.OAuthRefreshToken == "" {
		missing = append(missing, "GOOGLE_OAUTH_REFRESH_TOKEN")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %v", missing)
	}
	switch c.ActionMode {
	case ModeDryRun, ModeLabelOnly, ModeAutoTrash:
	default:
		return fmt.Errorf("invalid ACTION_MODE %q (want dry_run|label_only|auto_trash)", c.ActionMode)
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
