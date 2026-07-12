package weeklyreview

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/o-ga09/adk-go-sample/internal/store"
)

// fakeTaskStore is a canned store.TaskStore for BuildReport tests: it lets
// tests control UpdateTime/Due directly, which the real in-memory store
// (internal/store) does not expose a way to backdate.
type fakeTaskStore struct {
	tasks   []store.Task
	listErr error
}

func (f *fakeTaskStore) Create(context.Context, *store.Task) error { return nil }

func (f *fakeTaskStore) FindBySrcMessageID(context.Context, string) (*store.Task, error) {
	return nil, store.ErrTaskNotFound
}

func (f *fakeTaskStore) List(_ context.Context, filter store.TaskFilter) ([]store.Task, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []store.Task
	for _, t := range f.tasks {
		if filter.Status != "" && t.Status != filter.Status {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeTaskStore) Update(context.Context, string, store.TaskPatch) (*store.Task, error) {
	return nil, nil
}

func (f *fakeTaskStore) Complete(context.Context, string) (*store.Task, error) { return nil, nil }

func TestBuildReport(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	const staleDays = 14
	fresh := now.AddDate(0, 0, -1)
	stale := now.AddDate(0, 0, -20)
	dueSoon := now.AddDate(0, 0, 2)

	tasks := []store.Task{
		{ID: "inbox-1", Status: store.TaskStatusInbox, UpdateTime: fresh},
		{ID: "inbox-2", Status: store.TaskStatusInbox, UpdateTime: fresh},
		{ID: "next-fresh", Status: store.TaskStatusNext, UpdateTime: fresh, Due: &dueSoon},
		{ID: "next-stale", Status: store.TaskStatusNext, UpdateTime: stale},
		{ID: "waiting-stale", Status: store.TaskStatusWaiting, UpdateTime: stale},
		{ID: "waiting-fresh", Status: store.TaskStatusWaiting, UpdateTime: fresh},
		{ID: "done-stale", Status: store.TaskStatusDone, UpdateTime: stale},
	}
	st := &fakeTaskStore{tasks: tasks}

	report, err := BuildReport(context.Background(), st, now, staleDays)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if report.Date != "2026-07-12" {
		t.Errorf("Date = %q, want 2026-07-12", report.Date)
	}
	if report.InboxCount != 2 {
		t.Errorf("InboxCount = %d, want 2", report.InboxCount)
	}
	if report.StaleDays != staleDays {
		t.Errorf("StaleDays = %d, want %d", report.StaleDays, staleDays)
	}

	wantStalled := map[string]bool{"next-stale": true, "waiting-stale": true}
	if len(report.Stalled) != len(wantStalled) {
		t.Fatalf("Stalled = %+v, want %d items", report.Stalled, len(wantStalled))
	}
	for _, task := range report.Stalled {
		if !wantStalled[task.ID] {
			t.Errorf("unexpected stalled task %q", task.ID)
		}
	}

	wantNextUp := []string{"next-fresh", "next-stale"}
	if len(report.NextUp) != len(wantNextUp) {
		t.Fatalf("NextUp = %+v, want %d items", report.NextUp, len(wantNextUp))
	}
	for i, id := range wantNextUp {
		if report.NextUp[i].ID != id {
			t.Errorf("NextUp[%d] = %q, want %q", i, report.NextUp[i].ID, id)
		}
	}
}

func TestBuildReport_LimitsNextUp(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	var tasks []store.Task
	for i := range defaultNextUpLimit + 3 {
		tasks = append(tasks, store.Task{ID: string(rune('a' + i)), Status: store.TaskStatusNext, UpdateTime: now})
	}
	st := &fakeTaskStore{tasks: tasks}

	report, err := BuildReport(context.Background(), st, now, 14)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if len(report.NextUp) != defaultNextUpLimit {
		t.Errorf("len(NextUp) = %d, want %d", len(report.NextUp), defaultNextUpLimit)
	}
}

func TestBuildReport_PropagatesStoreErrors(t *testing.T) {
	st := &fakeTaskStore{listErr: errors.New("boom")}
	if _, err := BuildReport(context.Background(), st, time.Now(), 14); err == nil {
		t.Fatal("want error, got nil")
	}
}
