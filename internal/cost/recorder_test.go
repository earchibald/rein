package cost

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu        sync.Mutex
	events    []Event
	snapshots map[string]Snapshot
}

func newMemoryStore() *memoryStore {
	return &memoryStore{snapshots: map[string]Snapshot{}}
}

func (s *memoryStore) AppendCostEvent(_ context.Context, event Event) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	event.Sequence = int64(len(s.events) + 1)
	s.events = append(s.events, event)
	return event, nil
}

func (s *memoryStore) GetBudgetSnapshot(_ context.Context, scope Scope, scopeID string) (Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.snapshots[fmt.Sprintf("%s/%s", scope, scopeID)]
	return snapshot, ok, nil
}

func (s *memoryStore) UpsertBudgetSnapshot(_ context.Context, snapshot Snapshot) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[fmt.Sprintf("%s/%s", snapshot.Scope, snapshot.ScopeID)] = snapshot
	return snapshot, nil
}

func TestRecorderPublishesEventAndUpdatesSnapshots(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.May, 2, 15, 4, 5, 0, time.UTC)
	store := newMemoryStore()
	stream := NewStream()
	recorder := NewRecorder(store, WithPublisher(stream), WithNow(func() time.Time { return now }))
	streamCh, unsubscribe := stream.Subscribe(1)
	defer unsubscribe()

	ctx := context.Background()
	for _, scope := range []Scope{ScopeProject, ScopeIssue, ScopeExecution} {
		scopeID := map[Scope]string{
			ScopeProject:   "project-rein",
			ScopeIssue:     "RN-13",
			ScopeExecution: "exec-rn-13-001",
		}[scope]
		if _, err := recorder.ConfigureBudget(ctx, scope, scopeID, "USD", Limits{SoftLimitMicros: 1000, HardLimitMicros: 5000}); err != nil {
			t.Fatalf("ConfigureBudget(%s) error = %v", scope, err)
		}
	}

	result, err := recorder.Record(ctx, Event{
		ID:          "evt-1",
		Name:        "adapter.turn.completed",
		ProjectID:   "project-rein",
		IssueID:     "RN-13",
		ExecutionID: "exec-rn-13-001",
		WorkflowID:  "managed-issue-pr-review-merge",
		AdapterID:   "coding-copilot-fake",
		Currency:    "usd",
		CostMicros:  1200,
		Usage: Usage{
			InputTokens:  200,
			OutputTokens: 80,
		},
		Attributes: map[string]string{"workflow_step_id": "open-pr"},
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if len(result.Snapshots) != 3 {
		t.Fatalf("Record() snapshots = %d, want 3", len(result.Snapshots))
	}
	if result.Event.Sequence != 1 {
		t.Fatalf("Record() sequence = %d, want 1", result.Event.Sequence)
	}

	streamed := <-streamCh
	if streamed.ID != result.Event.ID {
		t.Fatalf("streamed event id = %q, want %q", streamed.ID, result.Event.ID)
	}
	if streamed.ResourceAttributes["rein.execution.id"] != "exec-rn-13-001" {
		t.Fatalf("streamed resource attrs = %+v", streamed.ResourceAttributes)
	}

	executionSnapshot, found, err := store.GetBudgetSnapshot(ctx, ScopeExecution, "exec-rn-13-001")
	if err != nil {
		t.Fatalf("GetBudgetSnapshot() error = %v", err)
	}
	if !found {
		t.Fatal("GetBudgetSnapshot() found = false, want true")
	}
	if !executionSnapshot.SoftLimited() || executionSnapshot.HardLimited() {
		t.Fatalf("execution snapshot limits = %+v", executionSnapshot)
	}
	if executionSnapshot.SpentMicros != 1200 || executionSnapshot.Usage.TotalTokens() != 280 {
		t.Fatalf("execution snapshot = %+v", executionSnapshot)
	}
}

func TestRecorderCheckLaunchBlocksOnlyAfterHardHit(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.May, 2, 16, 0, 0, 0, time.UTC)
	store := newMemoryStore()
	recorder := NewRecorder(store, WithNow(func() time.Time { return now }))
	ctx := context.Background()

	if _, err := recorder.ConfigureBudget(ctx, ScopeExecution, "exec-rn-13-001", "USD", Limits{HardLimitMicros: 1000}); err != nil {
		t.Fatalf("ConfigureBudget() error = %v", err)
	}

	before, err := recorder.CheckLaunch(ctx, ScopePath{ProjectID: "project-rein", IssueID: "RN-13", ExecutionID: "exec-rn-13-001"})
	if err != nil {
		t.Fatalf("CheckLaunch() before error = %v", err)
	}
	if !before.Allowed {
		t.Fatalf("CheckLaunch() before = %+v, want allowed", before)
	}

	if _, err := recorder.Record(ctx, Event{
		ID:          "evt-1",
		Name:        "adapter.turn.completed",
		ProjectID:   "project-rein",
		IssueID:     "RN-13",
		ExecutionID: "exec-rn-13-001",
		Currency:    "USD",
		CostMicros:  1200,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	after, err := recorder.CheckLaunch(ctx, ScopePath{ProjectID: "project-rein", IssueID: "RN-13", ExecutionID: "exec-rn-13-001"})
	if err != nil {
		t.Fatalf("CheckLaunch() after error = %v", err)
	}
	if after.Allowed || after.BlockedBy.Scope != ScopeExecution {
		t.Fatalf("CheckLaunch() after = %+v, want execution hard block", after)
	}
}
