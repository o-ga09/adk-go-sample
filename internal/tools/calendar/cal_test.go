package calendartools

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
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
