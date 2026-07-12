// Package slackbot lets the user invoke the Gmail secretary agent from Slack
// by @mentioning it. It connects to Slack over Socket Mode (an outbound
// WebSocket the bot itself opens), so no public HTTP endpoint or Events API
// signing secret is required. Each @mention's text is forwarded verbatim to
// the same agent used by cmd/batch and the ADK REST API, and the agent's
// final reply is posted back in the same Slack thread.
package slackbot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/o-ga09/adk-go-sample/internal/config"
	notifytools "github.com/o-ga09/adk-go-sample/internal/tools/notify"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Config carries the dependencies needed to run the Slack mention listener.
type Config struct {
	App             *config.Config
	Agent           agent.Agent
	SessionService  session.Service
	ArtifactService artifact.Service
}

// mentionRE strips a leading "<@U12345> " Slack mention token from a message,
// so the agent only sees the text the user actually typed after the mention.
var mentionRE = regexp.MustCompile(`^\s*<@[A-Z0-9]+>\s*`)

// Listener holds the running Slack Socket Mode connection and its ADK runner.
type Listener struct {
	app       *config.Config
	api       *slack.Client
	socket    *socketmode.Client
	run       *runner.Runner
	sessionSv session.Service
}

// New builds a Listener. It returns an error if the ADK runner cannot be
// constructed; Slack connectivity itself is only established once Run is
// called.
func New(cfg Config) (*Listener, error) {
	api := slack.New(cfg.App.SlackBotToken, slack.OptionAppLevelToken(cfg.App.SlackAppToken))
	socket := socketmode.New(api)

	r, err := runner.New(runner.Config{
		AppName:         cfg.App.AppName,
		Agent:           cfg.Agent,
		SessionService:  cfg.SessionService,
		ArtifactService: cfg.ArtifactService,
	})
	if err != nil {
		return nil, fmt.Errorf("create runner: %w", err)
	}

	return &Listener{
		app:       cfg.App,
		api:       api,
		socket:    socket,
		run:       r,
		sessionSv: cfg.SessionService,
	}, nil
}

// Run connects to Slack over Socket Mode and blocks, dispatching each
// app_mention event to handleMention, until ctx is cancelled or the
// connection fails permanently.
func (l *Listener) Run(ctx context.Context) error {
	go func() {
		for evt := range l.socket.Events {
			switch evt.Type {
			case socketmode.EventTypeConnecting:
				log.Print("slackbot: connecting to Slack Socket Mode")
			case socketmode.EventTypeConnected:
				log.Print("slackbot: connected to Slack Socket Mode")
			case socketmode.EventTypeConnectionError:
				log.Printf("slackbot: connection error: %v", evt.Data)
			case socketmode.EventTypeEventsAPI:
				eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					continue
				}
				if evt.Request != nil {
					l.socket.Ack(*evt.Request)
				}
				go l.handleEventsAPI(ctx, eventsAPIEvent)
			}
		}
	}()
	return l.socket.RunContext(ctx)
}

func (l *Listener) handleEventsAPI(ctx context.Context, outer slackevents.EventsAPIEvent) {
	if outer.InnerEvent.Type != "app_mention" {
		return
	}
	mention, ok := outer.InnerEvent.Data.(*slackevents.AppMentionEvent)
	if !ok || mention.BotID != "" {
		return // ignore malformed payloads and mentions triggered by other bots
	}

	if l.app.SlackAllowedUserID != "" && mention.User != l.app.SlackAllowedUserID {
		l.reply(ctx, mention, "すみません、このエージェントは登録済みユーザーのみ利用できます。")
		return
	}

	text := mentionRE.ReplaceAllString(mention.Text, "")
	text = strings.TrimSpace(text)
	if text == "" {
		l.reply(ctx, mention, "ご用件をメンションの後に書いてください。例: 「@agent 受信トレイを整理して」")
		return
	}

	reply, err := l.ask(ctx, mention, text)
	if err != nil {
		log.Printf("slackbot: agent run failed: %v", err)
		reply = fmt.Sprintf("エラーが発生しました: %v", err)
	}
	if reply == "" {
		return // summary already delivered into the thread by slack_push
	}
	l.reply(ctx, mention, reply)
}

// ask runs the agent with text as the user's message, reusing one ADK
// session per Slack thread so multi-turn conversations keep context.
func (l *Listener) ask(ctx context.Context, mention *slackevents.AppMentionEvent, text string) (string, error) {
	threadKey := mention.ThreadTimeStamp
	if threadKey == "" {
		threadKey = mention.TimeStamp
	}
	userID := mention.User
	sessionID := fmt.Sprintf("slack-%s-%s", mention.Channel, threadKey)

	if _, err := l.sessionSv.Get(ctx, &session.GetRequest{
		AppName:   l.app.AppName,
		UserID:    userID,
		SessionID: sessionID,
	}); err != nil {
		if _, err := l.sessionSv.Create(ctx, &session.CreateRequest{
			AppName:   l.app.AppName,
			UserID:    userID,
			SessionID: sessionID,
			// Record where this request came from so the notify tool posts
			// the summary back into the same thread instead of top-level to
			// SLACK_CHANNEL_ID.
			State: map[string]any{
				notifytools.StateKeySlackChannel:  mention.Channel,
				notifytools.StateKeySlackThreadTS: threadKey,
			},
		}); err != nil {
			return "", fmt.Errorf("create session: %w", err)
		}
	}

	msg := genai.NewContentFromText(text, genai.RoleUser)

	var lastText string
	var summaryPosted bool
	for ev, err := range l.run.Run(ctx, userID, sessionID, msg, agent.RunConfig{}) {
		if err != nil {
			return "", fmt.Errorf("agent run: %w", err)
		}
		if ev != nil && ev.Content != nil {
			for _, p := range ev.Content.Parts {
				if p.Text != "" {
					lastText = p.Text
				}
				if p.FunctionCall != nil {
					log.Printf("slackbot: [%s] tool-call: %s", ev.Author, p.FunctionCall.Name)
				}
				if p.FunctionResponse != nil {
					log.Printf("slackbot: [%s] tool-response: %s %s", ev.Author, p.FunctionResponse.Name, compactJSON(p.FunctionResponse.Response))
					if p.FunctionResponse.Name == notifytools.ToolNameSlackPush {
						if s, _ := p.FunctionResponse.Response["status"].(string); s == notifytools.StatusSlackPushSent {
							summaryPosted = true
						}
					}
				}
			}
		}
	}
	if lastText == "" {
		// For the mail-triage task the agent delivers its summary itself via
		// slack_push (into this same thread) and may end its turn without any
		// final text; replying "(応答がありませんでした)" on top of the summary
		// reads like a failure. Only fall back when nothing was delivered.
		if summaryPosted {
			return "", nil
		}
		lastText = "(応答がありませんでした)"
	}
	return lastText, nil
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

func (l *Listener) reply(ctx context.Context, mention *slackevents.AppMentionEvent, text string) {
	threadTS := mention.ThreadTimeStamp
	if threadTS == "" {
		threadTS = mention.TimeStamp
	}
	if _, _, err := l.api.PostMessageContext(ctx, mention.Channel, slack.MsgOptionText(text, false), slack.MsgOptionTS(threadTS)); err != nil {
		log.Printf("slackbot: failed to post reply: %v", err)
	}
}
