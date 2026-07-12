// Package weeklyreview builds the GTD weekly-review summary (unprocessed
// inbox items, stalled next/waiting tasks, and prioritized next actions) and
// posts it to Slack. See cmd/batch's "weekly-review" command for how it is
// invoked, and internal/llmusage's report.go/notify.go for the sibling
// daily-cost-report command this package's split mirrors.
package weeklyreview

import (
	"context"
	"fmt"
	"time"

	"github.com/o-ga09/adk-go-sample/internal/store"
)

// defaultNextUpLimit caps how many prioritized "next" tasks the weekly
// review suggests tackling, so the Slack message stays a short read rather
// than dumping the entire next-action list.
const defaultNextUpLimit = 5

// Report is one weekly review's aggregated GTD task state, ready to format
// for Slack.
type Report struct {
	Date      string // JST YYYY-MM-DD, the day the review was generated
	StaleDays int

	InboxCount int          // tasks still sitting unclarified in the inbox
	Stalled    []store.Task // next/waiting tasks not updated in >= StaleDays (GTD "stuck" items)
	NextUp     []store.Task // prioritized next actions to suggest this week, see store.SortByPriority
}

// BuildReport aggregates st's current GTD task state for a weekly review:
// how many items are still unclarified in the inbox, which next/waiting
// tasks have gone untouched for >= staleDays, and the prioritized next
// actions worth tackling this week (store.SortByPriority, capped at
// defaultNextUpLimit).
func BuildReport(ctx context.Context, st store.TaskStore, now time.Time, staleDays int) (Report, error) {
	inbox, err := st.List(ctx, store.TaskFilter{Status: store.TaskStatusInbox})
	if err != nil {
		return Report{}, fmt.Errorf("list inbox tasks: %w", err)
	}

	next, err := st.List(ctx, store.TaskFilter{Status: store.TaskStatusNext})
	if err != nil {
		return Report{}, fmt.Errorf("list next tasks: %w", err)
	}

	waiting, err := st.List(ctx, store.TaskFilter{Status: store.TaskStatusWaiting})
	if err != nil {
		return Report{}, fmt.Errorf("list waiting tasks: %w", err)
	}

	cutoff := now.AddDate(0, 0, -staleDays)
	var stalled []store.Task
	for _, t := range next {
		if t.UpdateTime.Before(cutoff) {
			stalled = append(stalled, t)
		}
	}
	for _, t := range waiting {
		if t.UpdateTime.Before(cutoff) {
			stalled = append(stalled, t)
		}
	}
	stalled = store.SortByPriority(stalled)

	nextUp := store.SortByPriority(next)
	if len(nextUp) > defaultNextUpLimit {
		nextUp = nextUp[:defaultNextUpLimit]
	}

	return Report{
		Date:       now.Format("2006-01-02"),
		StaleDays:  staleDays,
		InboxCount: len(inbox),
		Stalled:    stalled,
		NextUp:     nextUp,
	}, nil
}
