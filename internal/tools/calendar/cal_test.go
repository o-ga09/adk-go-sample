package calendartools

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	calendar "google.golang.org/api/calendar/v3"
)

// TestCreateInputSchemaAllowsOmittedOptionalFields replays the validation the
// ADK's functiontool runs on every call: fields without omitempty are required
// in the inferred schema, so the LLM omitting description/location/srcMessageId
// would fail the whole tool call.
func TestCreateInputSchemaAllowsOmittedOptionalFields(t *testing.T) {
	schema, err := jsonschema.For[createInput](nil)
	if err != nil {
		t.Fatalf("infer schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	payload := `{"summary":"定例MTG","startRFC3339":"2026-07-13T10:00:00+09:00","endRFC3339":"2026-07-13T11:00:00+09:00"}`
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(m); err != nil {
		t.Errorf("payload %s rejected by inferred schema: %v", payload, err)
	}
}

// TestListEventsInputSchemaAllowsOmittedFields replays the ADK's per-call
// validation: timeMinRFC3339/timeMaxRFC3339 must carry `omitempty` so the LLM
// can ask for "today" (the common case) without computing a window itself.
func TestListEventsInputSchemaAllowsOmittedFields(t *testing.T) {
	schema, err := jsonschema.For[listEventsInput](nil)
	if err != nil {
		t.Fatalf("infer schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	if err := resolved.Validate(map[string]any{}); err != nil {
		t.Errorf("empty payload {} rejected by inferred schema: %v", err)
	}
}

// TestListEventsResultSchemaAllowsNilEvents replays the ADK's per-call
// validation on the result side: a nil Events slice (a day with zero
// registered events) must serialize to an omitted field, not `null`, or the
// whole tool call fails validation despite the handler succeeding. See
// .claude/rules/tool-json-schema.md.
func TestListEventsResultSchemaAllowsNilEvents(t *testing.T) {
	schema, err := jsonschema.For[listEventsResult](nil)
	if err != nil {
		t.Fatalf("infer schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	result := listEventsResult{Status: "success"}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["events"]; ok {
		t.Fatalf("nil Events serialized with an `events` key (%v); want omitted", m["events"])
	}
	if err := resolved.Validate(m); err != nil {
		t.Errorf("zero-event result %s rejected by inferred schema: %v", b, err)
	}
}

func TestResolveWindow(t *testing.T) {
	// 2026-07-12T01:00:00Z is 2026-07-12T10:00:00+09:00: safely inside the
	// same JST calendar day, so the expected window is unambiguous.
	now := time.Date(2026, 7, 12, 1, 0, 0, 0, time.UTC)

	t.Run("両方省略なら本日のJST 0:00-24:00になる", func(t *testing.T) {
		timeMin, timeMax := resolveWindow(listEventsInput{}, now)
		if want := "2026-07-12T00:00:00+09:00"; timeMin != want {
			t.Errorf("timeMin = %q, want %q", timeMin, want)
		}
		if want := "2026-07-13T00:00:00+09:00"; timeMax != want {
			t.Errorf("timeMax = %q, want %q", timeMax, want)
		}
	})

	t.Run("指定があればそれを優先する", func(t *testing.T) {
		in := listEventsInput{
			TimeMinRFC3339: "2026-08-01T00:00:00+09:00",
			TimeMaxRFC3339: "2026-08-08T00:00:00+09:00",
		}
		timeMin, timeMax := resolveWindow(in, now)
		if timeMin != in.TimeMinRFC3339 {
			t.Errorf("timeMin = %q, want %q", timeMin, in.TimeMinRFC3339)
		}
		if timeMax != in.TimeMaxRFC3339 {
			t.Errorf("timeMax = %q, want %q", timeMax, in.TimeMaxRFC3339)
		}
	})
}

func TestEventTime(t *testing.T) {
	tests := []struct {
		name string
		in   *calendar.EventDateTime
		want string
	}{
		{name: "nil", in: nil, want: ""},
		{name: "時刻指定あり", in: &calendar.EventDateTime{DateTime: "2026-07-12T10:00:00+09:00"}, want: "2026-07-12T10:00:00+09:00"},
		{name: "終日イベント", in: &calendar.EventDateTime{Date: "2026-07-12"}, want: "2026-07-12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eventTime(tt.in); got != tt.want {
				t.Errorf("eventTime() = %q, want %q", got, tt.want)
			}
		})
	}
}
