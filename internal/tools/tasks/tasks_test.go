package tasktools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/o-ga09/adk-go-sample/internal/config"
	"github.com/o-ga09/adk-go-sample/internal/store"
)

func newTestStore(t *testing.T) store.TaskStore {
	t.Helper()
	st, err := store.NewTaskStore("")
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	return st
}

// mustValidate replays the validation the ADK's functiontool runs on every
// tool call/result: infer a schema from the struct, then validate the JSON
// payload against it. See .claude/rules/tool-json-schema.md.
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

func TestAddInputSchemaAllowsOmittedOptionalFields(t *testing.T) {
	mustValidate[addInput](t, `{"title":"水道代の支払い"}`)
}

func TestListInputSchemaAllowsOmittedFilters(t *testing.T) {
	mustValidate[listInput](t, `{}`)
}

func TestListResultSchemaAllowsZeroTasks(t *testing.T) {
	raw, err := json.Marshal(listResult{Status: "success"})
	if err != nil {
		t.Fatal(err)
	}
	mustValidate[listResult](t, string(raw))
}

func TestUpdateInputSchemaAllowsOmittedOptionalFields(t *testing.T) {
	mustValidate[updateInput](t, `{"id":"task-1"}`)
}

func TestTaskAdd_CreatesInboxTask(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	got := doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "重要メールの確認"})
	if got.Status != "created" {
		t.Fatalf("Status = %q, want created (err=%q)", got.Status, got.Error)
	}
	if got.ID == "" {
		t.Error("ID is empty")
	}
}

func TestTaskAdd_DedupesBySrcMessageID(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	first := doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "重要メールの確認", SrcMessageID: "msg-1"})
	if first.Status != "created" {
		t.Fatalf("first add Status = %q, want created", first.Status)
	}

	second := doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "重要メールの確認(重複)", SrcMessageID: "msg-1"})
	if second.Status != "already_exists" {
		t.Errorf("second add Status = %q, want already_exists", second.Status)
	}
	if second.ID != first.ID {
		t.Errorf("second add ID = %q, want %q", second.ID, first.ID)
	}

	tasks, err := st.List(ctx, store.TaskFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("List returned %d tasks, want 1 (no duplicate registered)", len(tasks))
	}
}

func TestTaskAdd_DryRunDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	got := doTaskAdd(ctx, st, config.ModeDryRun, addInput{Title: "重要メールの確認"})
	if got.Status != "dry_run_would_add" {
		t.Errorf("Status = %q, want dry_run_would_add", got.Status)
	}

	tasks, err := st.List(ctx, store.TaskFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("List returned %d tasks, want 0 in dry_run", len(tasks))
	}
}

func TestTaskAdd_InvalidDueReturnsError(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	got := doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "締め切りあり", Due: "not-a-date"})
	if got.Status != "error" {
		t.Errorf("Status = %q, want error", got.Status)
	}
}

func TestTaskList_FiltersByStatus(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "未整理タスク"})
	added := doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "次にやるタスク"})
	next := store.TaskStatusNext
	if _, err := st.Update(ctx, added.ID, store.TaskPatch{Status: &next}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := doTaskList(ctx, st, listInput{Status: "next"})
	if got.Status != "success" {
		t.Fatalf("Status = %q, want success (err=%q)", got.Status, got.Error)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].ID != added.ID {
		t.Errorf("Tasks = %+v, want only %q", got.Tasks, added.ID)
	}
}

func TestTaskList_OrdersByPriority(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	far := doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "来月の締め切り", Due: "2026-08-15T00:00:00Z"})
	none := doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "期限なし"})
	soon := doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "明日の締め切り", Due: "2026-07-13T00:00:00Z"})
	next := store.TaskStatusNext
	for _, id := range []string{far.ID, none.ID, soon.ID} {
		if _, err := st.Update(ctx, id, store.TaskPatch{Status: &next}); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}

	got := doTaskList(ctx, st, listInput{Status: "next"})
	if got.Status != "success" {
		t.Fatalf("Status = %q, want success (err=%q)", got.Status, got.Error)
	}
	wantOrder := []string{soon.ID, far.ID, none.ID}
	if len(got.Tasks) != len(wantOrder) {
		t.Fatalf("Tasks = %+v, want %d items", got.Tasks, len(wantOrder))
	}
	for i, id := range wantOrder {
		if got.Tasks[i].ID != id {
			t.Errorf("position %d = %q (%s), want %q", i, got.Tasks[i].ID, got.Tasks[i].Title, id)
		}
	}
}

func TestTaskList_InvalidStatusReturnsError(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	got := doTaskList(ctx, st, listInput{Status: "not-a-status"})
	if got.Status != "error" {
		t.Errorf("Status = %q, want error", got.Status)
	}
}

func TestTaskUpdate_ChangesFields(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	added := doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "企画書レビュー"})

	got := doTaskUpdate(ctx, st, config.ModeLabelOnly, updateInput{ID: added.ID, Status: "next", Context: "@pc"})
	if got.Status != "updated" {
		t.Fatalf("Status = %q, want updated (err=%q)", got.Status, got.Error)
	}

	tasks, err := st.List(ctx, store.TaskFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != store.TaskStatusNext || tasks[0].Context != "@pc" {
		t.Errorf("task after update = %+v, want status=next context=@pc", tasks)
	}
}

func TestTaskUpdate_DryRunDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	added := doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "企画書レビュー"})

	got := doTaskUpdate(ctx, st, config.ModeDryRun, updateInput{ID: added.ID, Status: "next"})
	if got.Status != "dry_run_would_update" {
		t.Errorf("Status = %q, want dry_run_would_update", got.Status)
	}

	tasks, err := st.List(ctx, store.TaskFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if tasks[0].Status != store.TaskStatusInbox {
		t.Errorf("Status = %q, want unchanged inbox in dry_run", tasks[0].Status)
	}
}

func TestTaskComplete_SetsStatusDone(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	added := doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "確定申告書の提出"})

	got := doTaskComplete(ctx, st, config.ModeLabelOnly, completeInput{ID: added.ID})
	if got.Status != "done" {
		t.Fatalf("Status = %q, want done (err=%q)", got.Status, got.Error)
	}

	tasks, err := st.List(ctx, store.TaskFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if tasks[0].Status != store.TaskStatusDone {
		t.Errorf("Status = %q, want done", tasks[0].Status)
	}
}

func TestTaskComplete_DryRunDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	added := doTaskAdd(ctx, st, config.ModeLabelOnly, addInput{Title: "確定申告書の提出"})

	got := doTaskComplete(ctx, st, config.ModeDryRun, completeInput{ID: added.ID})
	if got.Status != "dry_run_would_complete" {
		t.Errorf("Status = %q, want dry_run_would_complete", got.Status)
	}

	tasks, err := st.List(ctx, store.TaskFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if tasks[0].Status != store.TaskStatusInbox {
		t.Errorf("Status = %q, want unchanged inbox in dry_run", tasks[0].Status)
	}
}
