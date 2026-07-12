// Package tasktools exposes GTD (Getting Things Done) task management as ADK
// function tools: task_add captures an item into the inbox, task_update
// clarifies/organizes it (status/context/due/project), task_list surfaces
// tasks for review or "what should I do now", and task_complete finishes one.
package tasktools

import (
	"context"
	"fmt"
	"time"

	"github.com/o-ga09/adk-go-sample/internal/config"
	"github.com/o-ga09/adk-go-sample/internal/store"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// Tools returns the GTD task tools, wired to the given store and action
// mode. Task writes are not a mailbox/calendar mutation, but dry_run is an
// agent-wide rehearsal mode (see .claude/rules/action-mode-safety.md), so the
// mutating tools here honor it the same way the Gmail/Calendar tools do:
// logging the intended change and returning a dry_run_* status without
// writing.
func Tools(st store.TaskStore, mode config.ActionMode) ([]tool.Tool, error) {
	addTool, err := functiontool.New(functiontool.Config{
		Name: "task_add",
		Description: "Capture a new GTD task into the inbox. Provide a title; context/due/project/status are set " +
			"later via task_update once clarified. Pass srcMessageId when capturing from a triaged email so the " +
			"same message never creates a duplicate task.",
	}, taskAdd(st, mode))
	if err != nil {
		return nil, err
	}

	listTool, err := functiontool.New(functiontool.Config{
		Name: "task_list",
		Description: "List GTD tasks, optionally filtered by status (inbox/next/waiting/someday/done), context " +
			"(e.g. @home, @pc), or project. Results are ordered by priority: overdue tasks first (most overdue " +
			"first), then upcoming due dates soonest-first, then tasks with no due date (oldest-created first). " +
			"For a \"what should I do now\" suggestion, call with status=next and take the first few results.",
	}, taskList(st))
	if err != nil {
		return nil, err
	}

	updateTool, err := functiontool.New(functiontool.Config{
		Name: "task_update",
		Description: "Clarify/organize a task by id: set its status (inbox/next/waiting/someday/done), context, " +
			"due (RFC3339), and/or project. Only the fields provided are changed.",
	}, taskUpdate(st, mode))
	if err != nil {
		return nil, err
	}

	completeTool, err := functiontool.New(functiontool.Config{
		Name:        "task_complete",
		Description: "Mark a task done by id.",
	}, taskComplete(st, mode))
	if err != nil {
		return nil, err
	}

	return []tool.Tool{addTool, listTool, updateTool, completeTool}, nil
}

// ---- task_add ----

// Optional fields need `omitempty`: the inferred JSON schema marks fields
// without it as required, and the ADK rejects the whole call if the LLM
// omits one. Result slices must never serialize to null either. See
// .claude/rules/tool-json-schema.md.
type addInput struct {
	Title        string `json:"title"`
	Context      string `json:"context,omitempty"`
	Due          string `json:"due,omitempty"`
	Project      string `json:"project,omitempty"`
	SrcMessageID string `json:"srcMessageId,omitempty"`
}

type addResult struct {
	ID     string `json:"id,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func taskAdd(st store.TaskStore, mode config.ActionMode) functiontool.Func[addInput, addResult] {
	return func(ctx tool.Context, in addInput) addResult {
		return doTaskAdd(ctx, st, mode, in)
	}
}

// doTaskAdd holds task_add's logic as a plain function (context.Context, not
// tool.Context) so it is directly unit-testable without constructing an ADK
// tool.Context.
func doTaskAdd(ctx context.Context, st store.TaskStore, mode config.ActionMode, in addInput) addResult {
	if in.SrcMessageID != "" {
		if existing, err := st.FindBySrcMessageID(ctx, in.SrcMessageID); err == nil {
			return addResult{ID: existing.ID, Status: "already_exists"}
		}
	}

	due, err := parseDue(in.Due)
	if err != nil {
		return addResult{Status: "error", Error: err.Error()}
	}

	if mode == config.ModeDryRun {
		return addResult{Status: "dry_run_would_add"}
	}

	t := &store.Task{
		Title:        in.Title,
		Status:       store.TaskStatusInbox,
		Context:      in.Context,
		Due:          due,
		Project:      in.Project,
		SrcMessageID: in.SrcMessageID,
	}
	if err := st.Create(ctx, t); err != nil {
		return addResult{Status: "error", Error: err.Error()}
	}
	return addResult{ID: t.ID, Status: "created"}
}

// ---- task_list ----

type listInput struct {
	Status  string `json:"status,omitempty"`
	Context string `json:"context,omitempty"`
	Project string `json:"project,omitempty"`
}

type taskItem struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	Context      string `json:"context,omitempty"`
	Due          string `json:"due,omitempty"`
	Project      string `json:"project,omitempty"`
	SrcMessageID string `json:"srcMessageId,omitempty"`
}

type listResult struct {
	Tasks  []taskItem `json:"tasks,omitempty"`
	Status string     `json:"status"`
	Error  string     `json:"error,omitempty"`
}

func taskList(st store.TaskStore) functiontool.Func[listInput, listResult] {
	return func(ctx tool.Context, in listInput) listResult {
		return doTaskList(ctx, st, in)
	}
}

func doTaskList(ctx context.Context, st store.TaskStore, in listInput) listResult {
	status, err := parseStatus(in.Status)
	if err != nil {
		return listResult{Status: "error", Error: err.Error()}
	}
	tasks, err := st.List(ctx, store.TaskFilter{Status: status, Context: in.Context, Project: in.Project})
	if err != nil {
		return listResult{Status: "error", Error: err.Error()}
	}
	tasks = store.SortByPriority(tasks)
	out := listResult{Status: "success"}
	for _, t := range tasks {
		out.Tasks = append(out.Tasks, toTaskItem(t))
	}
	return out
}

// ---- task_update ----

type updateInput struct {
	ID      string `json:"id"`
	Status  string `json:"status,omitempty"`
	Context string `json:"context,omitempty"`
	Due     string `json:"due,omitempty"`
	Project string `json:"project,omitempty"`
}

type updateResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func taskUpdate(st store.TaskStore, mode config.ActionMode) functiontool.Func[updateInput, updateResult] {
	return func(ctx tool.Context, in updateInput) updateResult {
		return doTaskUpdate(ctx, st, mode, in)
	}
}

func doTaskUpdate(ctx context.Context, st store.TaskStore, mode config.ActionMode, in updateInput) updateResult {
	patch := store.TaskPatch{}
	if in.Status != "" {
		status, err := parseStatus(in.Status)
		if err != nil {
			return updateResult{Status: "error", Error: err.Error()}
		}
		patch.Status = &status
	}
	if in.Context != "" {
		patch.Context = &in.Context
	}
	if in.Project != "" {
		patch.Project = &in.Project
	}
	if in.Due != "" {
		due, err := parseDue(in.Due)
		if err != nil {
			return updateResult{Status: "error", Error: err.Error()}
		}
		patch.Due = due
	}

	if mode == config.ModeDryRun {
		return updateResult{Status: "dry_run_would_update"}
	}

	if _, err := st.Update(ctx, in.ID, patch); err != nil {
		return updateResult{Status: "error", Error: err.Error()}
	}
	return updateResult{Status: "updated"}
}

// ---- task_complete ----

type completeInput struct {
	ID string `json:"id"`
}

type completeResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func taskComplete(st store.TaskStore, mode config.ActionMode) functiontool.Func[completeInput, completeResult] {
	return func(ctx tool.Context, in completeInput) completeResult {
		return doTaskComplete(ctx, st, mode, in)
	}
}

func doTaskComplete(ctx context.Context, st store.TaskStore, mode config.ActionMode, in completeInput) completeResult {
	if mode == config.ModeDryRun {
		return completeResult{Status: "dry_run_would_complete"}
	}
	if _, err := st.Complete(ctx, in.ID); err != nil {
		return completeResult{Status: "error", Error: err.Error()}
	}
	return completeResult{Status: "done"}
}

// ---- helpers ----

func toTaskItem(t store.Task) taskItem {
	item := taskItem{
		ID:           t.ID,
		Title:        t.Title,
		Status:       string(t.Status),
		Context:      t.Context,
		Project:      t.Project,
		SrcMessageID: t.SrcMessageID,
	}
	if t.Due != nil {
		item.Due = t.Due.Format(time.RFC3339)
	}
	return item
}

func parseDue(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("invalid due (want RFC3339): %w", err)
	}
	return &t, nil
}

func parseStatus(s string) (store.TaskStatus, error) {
	if s == "" {
		return "", nil
	}
	switch store.TaskStatus(s) {
	case store.TaskStatusInbox, store.TaskStatusNext, store.TaskStatusWaiting, store.TaskStatusSomeday, store.TaskStatusDone:
		return store.TaskStatus(s), nil
	default:
		return "", fmt.Errorf("invalid status %q (want inbox|next|waiting|someday|done)", s)
	}
}
