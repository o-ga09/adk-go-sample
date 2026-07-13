// Package calendartools exposes Google Calendar operations as ADK function
// tools. Events are de-duplicated by the source Gmail message id stored in the
// event's private extended properties.
package calendartools

import (
	"time"

	"github.com/o-ga09/adk-go-sample/internal/config"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	calendar "google.golang.org/api/calendar/v3"
)

const primaryCalendar = "primary"
const srcMsgKey = "srcMsgId"

// Tools returns the calendar tools wired to the given service and action mode.
func Tools(svc *calendar.Service, mode config.ActionMode) ([]tool.Tool, error) {
	createTool, err := functiontool.New(functiontool.Config{
		Name: "calendar_create_event",
		Description: "Create an event on the primary Google Calendar. " +
			"Provide RFC3339 start/end times (e.g. 2026-06-18T10:00:00+09:00). " +
			"Pass the source Gmail message id to avoid creating duplicates.",
	}, createEvent(svc, mode))
	if err != nil {
		return nil, err
	}

	// Read-only, so it is registered regardless of ACTION_MODE: gating in
	// .claude/rules/action-mode-safety.md concerns mutating capability, and
	// listing events cannot mutate anything.
	listTool, err := functiontool.New(functiontool.Config{
		Name: "calendar_list_events",
		Description: "List events on the primary Google Calendar within a time window. " +
			"If timeMinRFC3339/timeMaxRFC3339 are omitted, defaults to today (JST, 00:00-24:00). " +
			"Returns title, start, end, and htmlLink for each event.",
	}, listEvents(svc))
	if err != nil {
		return nil, err
	}

	return []tool.Tool{createTool, listTool}, nil
}

// Optional fields need `omitempty`: the inferred JSON schema marks fields
// without it as required, and the ADK rejects the whole call if the LLM omits
// one. See .claude/rules/tool-json-schema.md.
type createInput struct {
	Summary      string `json:"summary"`
	StartRFC3339 string `json:"startRFC3339"`
	EndRFC3339   string `json:"endRFC3339"`
	Description  string `json:"description,omitempty"`
	Location     string `json:"location,omitempty"`
	SrcMessageID string `json:"srcMessageId,omitempty"`
}

type createResult struct {
	EventID string `json:"eventId"`
	HTMLink string `json:"htmlLink"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

func createEvent(svc *calendar.Service, mode config.ActionMode) functiontool.Func[createInput, createResult] {
	return func(ctx agent.Context, in createInput) (createResult, error) {
		// De-dup: if an event already references this message id, skip.
		if in.SrcMessageID != "" {
			existing, err := svc.Events.List(primaryCalendar).
				PrivateExtendedProperty(srcMsgKey + "=" + in.SrcMessageID).
				MaxResults(1).Context(ctx).Do()
			if err == nil && existing != nil && len(existing.Items) > 0 {
				return createResult{EventID: existing.Items[0].Id, Status: "already_exists"}, nil
			}
		}

		if mode == config.ModeDryRun {
			return createResult{Status: "dry_run_would_create"}, nil
		}

		event := &calendar.Event{
			Summary:     in.Summary,
			Description: in.Description,
			Location:    in.Location,
			Start:       &calendar.EventDateTime{DateTime: in.StartRFC3339},
			End:         &calendar.EventDateTime{DateTime: in.EndRFC3339},
		}
		if in.SrcMessageID != "" {
			event.ExtendedProperties = &calendar.EventExtendedProperties{
				Private: map[string]string{srcMsgKey: in.SrcMessageID},
			}
		}
		created, err := svc.Events.Insert(primaryCalendar, event).Context(ctx).Do()
		if err != nil {
			return createResult{Status: "error", Error: err.Error()}, nil
		}
		return createResult{EventID: created.Id, HTMLink: created.HtmlLink, Status: "created"}, nil
	}
}

// listEventsInput's fields are both optional: omitting one (or both) is the
// common case ("today"'s events), so both need `omitempty` or the LLM
// omitting them fails the whole call. See .claude/rules/tool-json-schema.md.
type listEventsInput struct {
	TimeMinRFC3339 string `json:"timeMinRFC3339,omitempty"`
	TimeMaxRFC3339 string `json:"timeMaxRFC3339,omitempty"`
}

type eventSummary struct {
	Title    string `json:"title"`
	Start    string `json:"start"`
	End      string `json:"end"`
	HTMLLink string `json:"htmlLink"`
}

// Events must carry `omitempty`: a nil slice on a day with zero events would
// otherwise serialize to `events: null`, which fails the ADK's inferred-schema
// validation despite the handler succeeding. See
// .claude/rules/tool-json-schema.md.
type listEventsResult struct {
	Events []eventSummary `json:"events,omitempty"`
	Status string         `json:"status"`
	Error  string         `json:"error,omitempty"`
}

func listEvents(svc *calendar.Service) functiontool.Func[listEventsInput, listEventsResult] {
	return func(ctx agent.Context, in listEventsInput) (listEventsResult, error) {
		timeMin, timeMax := resolveWindow(in, time.Now())
		resp, err := svc.Events.List(primaryCalendar).
			TimeMin(timeMin).TimeMax(timeMax).
			SingleEvents(true).OrderBy("startTime").
			Context(ctx).Do()
		if err != nil {
			return listEventsResult{Status: "error", Error: err.Error()}, nil
		}
		out := listEventsResult{Status: "success"}
		for _, e := range resp.Items {
			out.Events = append(out.Events, eventSummary{
				Title:    e.Summary,
				Start:    eventTime(e.Start),
				End:      eventTime(e.End),
				HTMLLink: e.HtmlLink,
			})
		}
		return out, nil
	}
}

// resolveWindow fills in any of timeMinRFC3339/timeMaxRFC3339 the caller
// omitted with the bounds of now's JST calendar day, so the LLM can ask for
// "today" without computing a window itself. now is passed in (rather than
// calling time.Now internally) so this stays deterministic under test.
func resolveWindow(in listEventsInput, now time.Time) (timeMin, timeMax string) {
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		loc = time.UTC
	}
	local := now.In(loc)
	startOfDay := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)

	timeMin = in.TimeMinRFC3339
	if timeMin == "" {
		timeMin = startOfDay.Format(time.RFC3339)
	}
	timeMax = in.TimeMaxRFC3339
	if timeMax == "" {
		timeMax = startOfDay.AddDate(0, 0, 1).Format(time.RFC3339)
	}
	return timeMin, timeMax
}

// eventTime renders a calendar.EventDateTime for display: the RFC3339
// DateTime for a timed event, or the plain Date for an all-day event.
func eventTime(dt *calendar.EventDateTime) string {
	if dt == nil {
		return ""
	}
	if dt.DateTime != "" {
		return dt.DateTime
	}
	return dt.Date
}
