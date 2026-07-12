// Package slackbot lets the user invoke the Gmail secretary agent from Slack
// by @mentioning it. It connects to Slack over Socket Mode (an outbound
// WebSocket the bot itself opens), so no public HTTP endpoint or Events API
// signing secret is required. Each @mention's text is forwarded verbatim to
// the same agent used by cmd/batch and the ADK REST API, and the agent's
// final reply is posted back in the same Slack thread. Once a thread has
// been started this way, plain messages in that thread (no @mention needed)
// keep talking to the same agent session too.
package slackbot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/o-ga09/adk-go-sample/internal/config"
	"github.com/o-ga09/adk-go-sample/internal/llmusage"
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
	// botUserID is this bot's own Slack user ID (e.g. "U0123456"), used to
	// recognize "<@botUserID>" mentions inside plain "message" events so a
	// message that mentions the bot isn't double-handled by both the
	// app_mention and message event for the same underlying Slack message.
	// Left empty (dedup skipped) if auth.test fails at startup.
	botUserID string
}

// New builds a Listener. It returns an error if the ADK runner cannot be
// constructed; Slack connectivity itself is only established once Run is
// called.
func New(ctx context.Context, cfg Config) (*Listener, error) {
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

	var botUserID string
	if auth, err := api.AuthTestContext(ctx); err != nil {
		log.Printf("slackbot: auth.test failed, thread replies without @mention will be ignored: %v", err)
	} else {
		botUserID = auth.UserID
	}

	return &Listener{
		app:       cfg.App,
		api:       api,
		socket:    socket,
		run:       r,
		sessionSv: cfg.SessionService,
		botUserID: botUserID,
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

// incomingMessage is a normalized view over the two Slack event types this
// listener responds to (app_mention and plain thread replies), so ask/reply
// don't need to know which one triggered them.
type incomingMessage struct {
	Channel         string
	User            string
	TimeStamp       string
	ThreadTimeStamp string
}

// threadKey identifies the Slack thread a message belongs to: its own
// ThreadTimeStamp, or its own TimeStamp if it's not part of a thread (e.g. a
// top-level @mention, which starts a new thread rooted at itself).
func (m incomingMessage) threadKey() string {
	if m.ThreadTimeStamp != "" {
		return m.ThreadTimeStamp
	}
	return m.TimeStamp
}

// sessionID is the ADK session this message's thread is tracked under; see
// ask for why one session is reused per Slack thread.
func (m incomingMessage) sessionID() string {
	return fmt.Sprintf("slack-%s-%s", m.Channel, m.threadKey())
}

func (l *Listener) handleEventsAPI(ctx context.Context, outer slackevents.EventsAPIEvent) {
	switch outer.InnerEvent.Type {
	case "app_mention":
		l.handleAppMention(ctx, outer)
	case "message":
		l.handleThreadReply(ctx, outer)
	}
}

// parseAppMention normalizes an AppMentionEvent into an incomingMessage plus
// the user's text with the leading mention token stripped. ok is false for
// malformed payloads and for mentions triggered by other bots (BotID set).
// The returned text may be empty (a bare "@bot" with nothing after it); the
// caller decides how to respond to that, since it differs from the "ignore
// entirely" cases that make ok false.
func parseAppMention(mention *slackevents.AppMentionEvent) (msg incomingMessage, text string, ok bool) {
	if mention == nil || mention.BotID != "" {
		return incomingMessage{}, "", false
	}
	msg = incomingMessage{
		Channel:         mention.Channel,
		User:            mention.User,
		TimeStamp:       mention.TimeStamp,
		ThreadTimeStamp: mention.ThreadTimeStamp,
	}
	text = strings.TrimSpace(mentionRE.ReplaceAllString(mention.Text, ""))
	return msg, text, true
}

func (l *Listener) handleAppMention(ctx context.Context, outer slackevents.EventsAPIEvent) {
	mention, ok := outer.InnerEvent.Data.(*slackevents.AppMentionEvent)
	if !ok {
		return
	}
	msg, text, ok := parseAppMention(mention)
	if !ok {
		return // ignore malformed payloads and mentions triggered by other bots
	}

	if l.app.SlackAllowedUserID != "" && msg.User != l.app.SlackAllowedUserID {
		l.reply(ctx, msg, "すみません、このエージェントは登録済みユーザーのみ利用できます。")
		return
	}

	if text == "" {
		l.reply(ctx, msg, "ご用件をメンションの後に書いてください。例: 「@agent 受信トレイを整理して」")
		return
	}

	l.respond(ctx, msg, text)
}

// parseThreadReply normalizes a plain "message" event into an incomingMessage
// plus its text, or reports ok=false when the event should be ignored:
// bot-authored messages (BotID set, e.g. this bot's own replies echoed back),
// any non-empty SubType (edits, deletes, channel-join notices, ...), messages
// that aren't a reply within a thread, a thread's own root message, a message
// that also mentions this bot (Slack delivers that as a separate app_mention
// event, which is handled by handleAppMention instead — treating it here too
// would run the agent twice for one Slack message), and blank text.
func parseThreadReply(ev *slackevents.MessageEvent, botUserID string) (msg incomingMessage, text string, ok bool) {
	if ev == nil || ev.BotID != "" || ev.SubType != "" {
		return incomingMessage{}, "", false
	}
	if ev.ThreadTimeStamp == "" || ev.ThreadTimeStamp == ev.TimeStamp {
		return incomingMessage{}, "", false
	}
	if botUserID != "" && strings.Contains(ev.Text, "<@"+botUserID+">") {
		return incomingMessage{}, "", false
	}
	text = strings.TrimSpace(ev.Text)
	if text == "" {
		return incomingMessage{}, "", false
	}
	msg = incomingMessage{
		Channel:         ev.Channel,
		User:            ev.User,
		TimeStamp:       ev.TimeStamp,
		ThreadTimeStamp: ev.ThreadTimeStamp,
	}
	return msg, text, true
}

// handleThreadReply lets a user keep talking to the agent in a thread
// without re-@mentioning it every time, once that thread already has an ADK
// session (created by an initial @mention). Threads the bot was never
// mentioned in are left alone, so it doesn't start responding to unrelated
// conversations it happens to receive message events for.
func (l *Listener) handleThreadReply(ctx context.Context, outer slackevents.EventsAPIEvent) {
	ev, ok := outer.InnerEvent.Data.(*slackevents.MessageEvent)
	if !ok {
		return
	}
	msg, text, ok := parseThreadReply(ev, l.botUserID)
	if !ok {
		return
	}

	if l.app.SlackAllowedUserID != "" && msg.User != l.app.SlackAllowedUserID {
		return // stay silent for other users rather than replying into a thread they may not own
	}

	if !l.hasSession(ctx, msg) {
		return // this thread was never started with an @mention; don't self-invite
	}

	l.respond(ctx, msg, text)
}

// hasSession reports whether msg's thread already has an ADK session, i.e.
// whether it was previously started with an @mention.
func (l *Listener) hasSession(ctx context.Context, msg incomingMessage) bool {
	_, err := l.sessionSv.Get(ctx, &session.GetRequest{
		AppName:   l.app.AppName,
		UserID:    msg.User,
		SessionID: msg.sessionID(),
	})
	return err == nil
}

// respond runs the agent on text and posts its reply (if any) back into
// msg's thread.
func (l *Listener) respond(ctx context.Context, msg incomingMessage, text string) {
	reply, err := l.ask(ctx, msg, text)
	if err != nil {
		log.Printf("slackbot: agent run failed: %v", err)
		reply = fmt.Sprintf("エラーが発生しました: %v", err)
	}
	if reply == "" {
		return // already delivered into the thread by a delivery tool (see isDeliveryTool)
	}
	l.reply(ctx, msg, reply)
}

// ask runs the agent with text as the user's message, reusing one ADK
// session per Slack thread so multi-turn conversations keep context.
func (l *Listener) ask(ctx context.Context, msg incomingMessage, text string) (string, error) {
	// Tags every LLM call made while handling this request as
	// llmusage.TriggerSlack instead of the default TriggerAPI, so the daily
	// cost report can break usage down by trigger surface.
	ctx = llmusage.WithTrigger(ctx, llmusage.TriggerSlack)

	threadKey := msg.threadKey()
	userID := msg.User
	sessionID := msg.sessionID()

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
				notifytools.StateKeySlackChannel:  msg.Channel,
				notifytools.StateKeySlackThreadTS: threadKey,
			},
		}); err != nil {
			return "", fmt.Errorf("create session: %w", err)
		}
	}

	content := genai.NewContentFromText(text, genai.RoleUser)

	var lastText string
	var summaryPosted bool
	for ev, err := range l.run.Run(ctx, userID, sessionID, content, agent.RunConfig{}) {
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
					if isDeliveryTool(p.FunctionResponse.Name) {
						if s, _ := p.FunctionResponse.Response["status"].(string); s == notifytools.StatusSlackPushSent {
							summaryPosted = true
						}
					}
				}
			}
		}
	}
	if lastText == "" {
		// For the mail-triage and calendar-digest tasks the agent delivers its
		// reply itself via a delivery tool (see isDeliveryTool, into this same
		// thread) and may end its turn without any final text; replying
		// "(応答がありませんでした)" on top of that reads like a failure. Only
		// fall back when nothing was delivered.
		if summaryPosted {
			return "", nil
		}
		lastText = "(応答がありませんでした)"
	}
	return lastText, nil
}

// deliveryToolNames are the notify tools that post directly into the
// requesting Slack thread/channel themselves (slack_push for mail triage,
// calendar_digest_push for #14's calendar query/digest), as opposed to
// answering via the agent's final response text. ask() must not follow one of
// these with a "(応答がありませんでした)" fallback reply.
var deliveryToolNames = map[string]bool{
	notifytools.ToolNameSlackPush:          true,
	notifytools.ToolNameCalendarDigestPush: true,
}

// isDeliveryTool reports whether name is one of deliveryToolNames.
func isDeliveryTool(name string) bool {
	return deliveryToolNames[name]
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

func (l *Listener) reply(ctx context.Context, msg incomingMessage, text string) {
	if _, _, err := l.api.PostMessageContext(ctx, msg.Channel, slack.MsgOptionText(text, false), slack.MsgOptionTS(msg.threadKey())); err != nil {
		log.Printf("slackbot: failed to post reply: %v", err)
	}
}
