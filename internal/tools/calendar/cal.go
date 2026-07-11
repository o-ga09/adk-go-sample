// Package calendartools exposes Google Calendar operations as ADK function
// tools. Events are de-duplicated by the source Gmail message id stored in the
// event's private extended properties.
package calendartools

import (
	"github.com/o-ga09/adk-go-sample/internal/config"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
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
	return []tool.Tool{createTool}, nil
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
	return func(ctx tool.Context, in createInput) createResult {
		// De-dup: if an event already references this message id, skip.
		if in.SrcMessageID != "" {
			existing, err := svc.Events.List(primaryCalendar).
				PrivateExtendedProperty(srcMsgKey + "=" + in.SrcMessageID).
				MaxResults(1).Context(ctx).Do()
			if err == nil && existing != nil && len(existing.Items) > 0 {
				return createResult{EventID: existing.Items[0].Id, Status: "already_exists"}
			}
		}

		if mode == config.ModeDryRun {
			return createResult{Status: "dry_run_would_create"}
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
			return createResult{Status: "error", Error: err.Error()}
		}
		return createResult{EventID: created.Id, HTMLink: created.HtmlLink, Status: "created"}
	}
}
