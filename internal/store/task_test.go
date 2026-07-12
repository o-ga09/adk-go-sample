package store

import (
	"context"
	"testing"
	"time"
)

// newTestTaskStore returns an in-memory TaskStore (empty dsn), the same
// fallback NewSessionService uses for local development without MySQL. No
// MySQL-backed test exists here because the project has no test database
// available in CI (see internal/tools/*'s jsonschema-only tests).
func newTestTaskStore(t *testing.T) TaskStore {
	t.Helper()
	st, err := NewTaskStore("")
	if err != nil {
		t.Fatalf("NewTaskStore(\"\"): %v", err)
	}
	return st
}

func TestTaskStore_CreateAssignsIDAndInboxStatus(t *testing.T) {
	ctx := context.Background()
	st := newTestTaskStore(t)

	task := &Task{Title: "水道代の支払い"}
	if err := st.Create(ctx, task); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.ID == "" {
		t.Error("Create did not assign an ID")
	}
	if task.Status != TaskStatusInbox {
		t.Errorf("Status = %q, want %q", task.Status, TaskStatusInbox)
	}
}

func TestTaskStore_FindBySrcMessageID(t *testing.T) {
	ctx := context.Background()
	st := newTestTaskStore(t)

	task := &Task{Title: "重要メールの確認", SrcMessageID: "msg-1"}
	if err := st.Create(ctx, task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	tests := []struct {
		name         string
		srcMessageID string
		wantFound    bool
	}{
		{name: "存在するsrcMessageIdで見つかる", srcMessageID: "msg-1", wantFound: true},
		{name: "存在しないsrcMessageIdはErrTaskNotFound", srcMessageID: "msg-unknown", wantFound: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := st.FindBySrcMessageID(ctx, tt.srcMessageID)
			if tt.wantFound {
				if err != nil {
					t.Fatalf("FindBySrcMessageID: %v", err)
				}
				if got.ID != task.ID {
					t.Errorf("ID = %q, want %q", got.ID, task.ID)
				}
				return
			}
			if err != ErrTaskNotFound {
				t.Errorf("err = %v, want ErrTaskNotFound", err)
			}
		})
	}
}

func TestTaskStore_ListFiltersByStatusContextProject(t *testing.T) {
	ctx := context.Background()
	st := newTestTaskStore(t)

	inboxTask := &Task{Title: "整理前"}
	nextHomeTask := &Task{Title: "掃除する", Status: TaskStatusNext, Context: "@home", Project: "生活"}
	nextPCTask := &Task{Title: "レポート作成", Status: TaskStatusNext, Context: "@pc", Project: "仕事"}
	for _, task := range []*Task{inboxTask, nextHomeTask, nextPCTask} {
		if err := st.Create(ctx, task); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	tests := []struct {
		name   string
		filter TaskFilter
		want   []string
	}{
		{name: "フィルタなしは全件", filter: TaskFilter{}, want: []string{inboxTask.ID, nextHomeTask.ID, nextPCTask.ID}},
		{name: "statusで絞り込み", filter: TaskFilter{Status: TaskStatusNext}, want: []string{nextHomeTask.ID, nextPCTask.ID}},
		{name: "contextで絞り込み", filter: TaskFilter{Context: "@home"}, want: []string{nextHomeTask.ID}},
		{name: "projectで絞り込み", filter: TaskFilter{Project: "仕事"}, want: []string{nextPCTask.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := st.List(ctx, tt.filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("List returned %d tasks, want %d", len(got), len(tt.want))
			}
			ids := make(map[string]bool, len(got))
			for _, task := range got {
				ids[task.ID] = true
			}
			for _, id := range tt.want {
				if !ids[id] {
					t.Errorf("List result missing task %q", id)
				}
			}
		})
	}
}

func TestTaskStore_UpdatePatchesOnlyGivenFields(t *testing.T) {
	ctx := context.Background()
	st := newTestTaskStore(t)

	task := &Task{Title: "企画書レビュー", Project: "新規事業"}
	if err := st.Create(ctx, task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	next := TaskStatusNext
	newContext := "@pc"
	due := time.Date(2026, 7, 20, 18, 0, 0, 0, time.UTC)
	updated, err := st.Update(ctx, task.ID, TaskPatch{Status: &next, Context: &newContext, Due: &due})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Status != TaskStatusNext {
		t.Errorf("Status = %q, want %q", updated.Status, TaskStatusNext)
	}
	if updated.Context != "@pc" {
		t.Errorf("Context = %q, want @pc", updated.Context)
	}
	if updated.Due == nil || !updated.Due.Equal(due) {
		t.Errorf("Due = %v, want %v", updated.Due, due)
	}
	if updated.Project != "新規事業" {
		t.Errorf("Project = %q, want unchanged 新規事業", updated.Project)
	}
}

func TestTaskStore_UpdateUnknownIDReturnsErrTaskNotFound(t *testing.T) {
	ctx := context.Background()
	st := newTestTaskStore(t)

	next := TaskStatusNext
	if _, err := st.Update(ctx, "no-such-id", TaskPatch{Status: &next}); err != ErrTaskNotFound {
		t.Errorf("err = %v, want ErrTaskNotFound", err)
	}
}

func TestSortByPriority(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	dueSoon := base.AddDate(0, 0, 1)
	dueLater := base.AddDate(0, 0, 10)
	overdue := base.AddDate(0, 0, -1)

	noDueOld := Task{ID: "no-due-old", CreateTime: base}
	noDueNew := Task{ID: "no-due-new", CreateTime: base.AddDate(0, 0, 1)}
	dueLaterTask := Task{ID: "due-later", Due: &dueLater, CreateTime: base}
	dueSoonTask := Task{ID: "due-soon", Due: &dueSoon, CreateTime: base}
	overdueTask := Task{ID: "overdue", Due: &overdue, CreateTime: base}

	got := SortByPriority([]Task{noDueNew, dueLaterTask, noDueOld, dueSoonTask, overdueTask})

	want := []string{"overdue", "due-soon", "due-later", "no-due-old", "no-due-new"}
	if len(got) != len(want) {
		t.Fatalf("SortByPriority returned %d tasks, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("position %d = %q, want %q (order: %v)", i, got[i].ID, id, taskIDs(got))
		}
	}
}

func taskIDs(tasks []Task) []string {
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	return ids
}

func TestTaskStore_CompleteSetsStatusDone(t *testing.T) {
	ctx := context.Background()
	st := newTestTaskStore(t)

	task := &Task{Title: "確定申告書の提出"}
	if err := st.Create(ctx, task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := st.Complete(ctx, task.ID)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if updated.Status != TaskStatusDone {
		t.Errorf("Status = %q, want %q", updated.Status, TaskStatusDone)
	}
}
