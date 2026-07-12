package store

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TaskStatus is a task's position in the GTD workflow: captured into Inbox,
// clarified into Next/Waiting/Someday, and finally Done.
type TaskStatus string

const (
	TaskStatusInbox   TaskStatus = "inbox"
	TaskStatusNext    TaskStatus = "next"
	TaskStatusWaiting TaskStatus = "waiting"
	TaskStatusSomeday TaskStatus = "someday"
	TaskStatusDone    TaskStatus = "done"
)

// Task is one GTD item captured by task_add and refined by task_update.
// SrcMessageID, when set, is the Gmail message id the task was captured
// from; task_add uses it to avoid registering the same mail twice.
type Task struct {
	ID           string `gorm:"primaryKey"`
	Title        string
	Status       TaskStatus `gorm:"index"`
	Context      string
	Due          *time.Time `gorm:"type:datetime(6);precision:6"`
	Project      string
	SrcMessageID string    `gorm:"index"`
	CreateTime   time.Time `gorm:"type:datetime(6);precision:6;autoCreateTime"`
	UpdateTime   time.Time `gorm:"type:datetime(6);precision:6;autoUpdateTime"`
}

// TableName pins the GORM table name (see .claude/rules/mysql-sessions.md for
// why the datetime(6)/precision:6 pair above matters for this project).
func (Task) TableName() string { return "tasks" }

// ErrTaskNotFound is returned by FindBySrcMessageID and Update when no task
// matches.
var ErrTaskNotFound = errors.New("task not found")

// TaskPatch carries the fields task_update may change. A nil field means
// "leave unchanged" so a partial update never clobbers the others.
type TaskPatch struct {
	Status  *TaskStatus
	Context *string
	Due     *time.Time
	Project *string
}

// TaskFilter narrows TaskStore.List. The zero value lists every task.
type TaskFilter struct {
	Status  TaskStatus
	Context string
	Project string
}

// TaskStore persists GTD tasks for the tasktools package.
type TaskStore interface {
	Create(ctx context.Context, t *Task) error
	FindBySrcMessageID(ctx context.Context, srcMessageID string) (*Task, error)
	List(ctx context.Context, filter TaskFilter) ([]Task, error)
	Update(ctx context.Context, id string, patch TaskPatch) (*Task, error)
	Complete(ctx context.Context, id string) (*Task, error)
}

// NewTaskStore returns a MySQL-backed TaskStore when dsn is non-empty,
// otherwise an in-memory one — the same dsn-empty-means-in-memory fallback
// NewSessionService uses, so tasks work locally without a database.
func NewTaskStore(dsn string) (TaskStore, error) {
	if dsn == "" {
		return newMemoryTaskStore(), nil
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	return &gormTaskStore{db: db}, nil
}

// SortByPriority returns tasks ordered for a "what should I do now"
// suggestion: overdue tasks first (most overdue first), then upcoming due
// dates soonest-first, then tasks with no due date at all (oldest-created
// first, GTD's usual FIFO tiebreak). It does not mutate tasks.
func SortByPriority(tasks []Task) []Task {
	out := make([]Task, len(tasks))
	copy(out, tasks)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Due == nil) != (b.Due == nil) {
			return a.Due != nil // a task with a due date always outranks one without
		}
		if a.Due != nil {
			return a.Due.Before(*b.Due)
		}
		return a.CreateTime.Before(b.CreateTime)
	})
	return out
}

func applyTaskPatch(t *Task, patch TaskPatch) {
	if patch.Status != nil {
		t.Status = *patch.Status
	}
	if patch.Context != nil {
		t.Context = *patch.Context
	}
	if patch.Due != nil {
		t.Due = patch.Due
	}
	if patch.Project != nil {
		t.Project = *patch.Project
	}
}

// ---- MySQL-backed store ----

type gormTaskStore struct{ db *gorm.DB }

func (s *gormTaskStore) Create(ctx context.Context, t *Task) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.Status == "" {
		t.Status = TaskStatusInbox
	}
	return s.db.WithContext(ctx).Create(t).Error
}

func (s *gormTaskStore) FindBySrcMessageID(ctx context.Context, srcMessageID string) (*Task, error) {
	var t Task
	err := s.db.WithContext(ctx).Where("src_message_id = ?", srcMessageID).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *gormTaskStore) List(ctx context.Context, filter TaskFilter) ([]Task, error) {
	q := s.db.WithContext(ctx).Model(&Task{})
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.Context != "" {
		q = q.Where("context = ?", filter.Context)
	}
	if filter.Project != "" {
		q = q.Where("project = ?", filter.Project)
	}
	var tasks []Task
	if err := q.Order("create_time asc").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *gormTaskStore) Update(ctx context.Context, id string, patch TaskPatch) (*Task, error) {
	var t Task
	if err := s.db.WithContext(ctx).First(&t, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTaskNotFound
		}
		return nil, err
	}
	applyTaskPatch(&t, patch)
	if err := s.db.WithContext(ctx).Save(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *gormTaskStore) Complete(ctx context.Context, id string) (*Task, error) {
	done := TaskStatusDone
	return s.Update(ctx, id, TaskPatch{Status: &done})
}

// ---- in-memory store (local dev without MYSQL_DSN) ----

type memoryTaskStore struct {
	mu    sync.Mutex
	tasks map[string]Task
}

func newMemoryTaskStore() *memoryTaskStore {
	return &memoryTaskStore{tasks: make(map[string]Task)}
}

func (s *memoryTaskStore) Create(ctx context.Context, t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.Status == "" {
		t.Status = TaskStatusInbox
	}
	now := time.Now()
	t.CreateTime = now
	t.UpdateTime = now
	s.tasks[t.ID] = *t
	return nil
}

func (s *memoryTaskStore) FindBySrcMessageID(ctx context.Context, srcMessageID string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tasks {
		if t.SrcMessageID == srcMessageID {
			found := t
			return &found, nil
		}
	}
	return nil, ErrTaskNotFound
}

func (s *memoryTaskStore) List(ctx context.Context, filter TaskFilter) ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		if filter.Status != "" && t.Status != filter.Status {
			continue
		}
		if filter.Context != "" && t.Context != filter.Context {
			continue
		}
		if filter.Project != "" && t.Project != filter.Project {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreateTime.Before(out[j].CreateTime) })
	return out, nil
}

func (s *memoryTaskStore) Update(ctx context.Context, id string, patch TaskPatch) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	applyTaskPatch(&t, patch)
	t.UpdateTime = time.Now()
	s.tasks[id] = t
	found := t
	return &found, nil
}

func (s *memoryTaskStore) Complete(ctx context.Context, id string) (*Task, error) {
	done := TaskStatusDone
	return s.Update(ctx, id, TaskPatch{Status: &done})
}
