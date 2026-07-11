package gmailtools

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// mustValidate replays the validation the ADK's functiontool runs on every
// tool call/result: infer a schema from the struct, then validate the JSON
// payload against it. A failure here means the ADK would reject the whole
// call at runtime (this is how the batch's "メール取得失敗" incident happened).
func mustValidate[T any](t *testing.T, payload string) {
	t.Helper()
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		t.Fatalf("infer schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if err := resolved.Validate(m); err != nil {
		t.Errorf("payload %s rejected by inferred schema: %v", payload, err)
	}
}

func TestListInputSchemaAllowsOmittedMaxResults(t *testing.T) {
	mustValidate[listInput](t, `{"query":"in:inbox is:unread newer_than:1d"}`)
}

func TestListResultSchemaAllowsZeroMessages(t *testing.T) {
	// A nil Messages slice must not serialize to null: {"messages":null,...}
	// fails schema validation and the ADK drops the entire result.
	raw, err := json.Marshal(listResult{Status: "success"})
	if err != nil {
		t.Fatal(err)
	}
	mustValidate[listResult](t, string(raw))
}

func TestListResultSchemaAllowsErrorResult(t *testing.T) {
	raw, err := json.Marshal(listResult{Status: "error", Error: "googleapi: Error 401"})
	if err != nil {
		t.Fatal(err)
	}
	mustValidate[listResult](t, string(raw))
}

func TestApplyLabelInputSchemaAllowsOmittedRemoveFromInbox(t *testing.T) {
	mustValidate[applyLabelInput](t, `{"messageId":"m1","labelName":"secretary/unwanted"}`)
}
