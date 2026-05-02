package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/earchibald/rein/internal/cost"
)

func TestStoreCostTelemetryRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	first, err := store.AppendCostEvent(ctx, cost.Event{
		ID:          "evt-1",
		Name:        "adapter.turn.completed",
		Time:        time.Date(2026, time.May, 2, 18, 0, 0, 0, time.UTC),
		ProjectID:   "project-rein",
		IssueID:     "RN-13",
		ExecutionID: "exec-rn-13-001",
		AdapterID:   "coding-copilot-fake",
		Currency:    "USD",
		CostMicros:  1200,
		Usage: cost.Usage{
			InputTokens:  200,
			OutputTokens: 80,
		},
	})
	if err != nil {
		t.Fatalf("AppendCostEvent() first error = %v", err)
	}
	second, err := store.AppendCostEvent(ctx, cost.Event{
		ID:          "evt-2",
		Name:        "adapter.turn.completed",
		Time:        time.Date(2026, time.May, 2, 18, 1, 0, 0, time.UTC),
		ProjectID:   "project-rein",
		IssueID:     "RN-13",
		ExecutionID: "exec-rn-13-001",
		AdapterID:   "review-bot-fake",
		Currency:    "USD",
		CostMicros:  900,
	})
	if err != nil {
		t.Fatalf("AppendCostEvent() second error = %v", err)
	}

	events, err := store.ListCostEvents(ctx, CostEventFilter{ExecutionID: "exec-rn-13-001"})
	if err != nil {
		t.Fatalf("ListCostEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("ListCostEvents() count = %d, want 2", len(events))
	}
	if events[0].ID != first.ID || events[0].Sequence != 1 {
		t.Fatalf("ListCostEvents()[0] = %+v", events[0])
	}
	if events[1].ID != second.ID || events[1].Sequence != 2 {
		t.Fatalf("ListCostEvents()[1] = %+v", events[1])
	}
}

func TestStoreBudgetSnapshotRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	updatedAt := time.Date(2026, time.May, 2, 19, 0, 0, 0, time.UTC)

	created, err := store.UpsertBudgetSnapshot(ctx, cost.Snapshot{
		Scope:            cost.ScopeExecution,
		ScopeID:          "exec-rn-13-001",
		Currency:         "USD",
		SoftLimitMicros:  1000,
		HardLimitMicros:  5000,
		SpentMicros:      1200,
		Usage:            cost.Usage{InputTokens: 200, OutputTokens: 80},
		EventCount:       1,
		LastEventID:      "evt-1",
		LastEventTime:    updatedAt,
		SoftLimitHitTime: updatedAt,
		UpdatedAt:        updatedAt,
	})
	if err != nil {
		t.Fatalf("UpsertBudgetSnapshot() create error = %v", err)
	}

	got, found, err := store.GetBudgetSnapshot(ctx, cost.ScopeExecution, "exec-rn-13-001")
	if err != nil {
		t.Fatalf("GetBudgetSnapshot() error = %v", err)
	}
	if !found {
		t.Fatal("GetBudgetSnapshot() found = false, want true")
	}
	if got.SpentMicros != created.SpentMicros || got.LastEventID != created.LastEventID {
		t.Fatalf("GetBudgetSnapshot() = %+v, want %+v", got, created)
	}

	got.SpentMicros = 5200
	got.EventCount = 2
	got.LastEventID = "evt-2"
	got.HardLimitHitTime = updatedAt.Add(time.Minute)
	got.UpdatedAt = updatedAt.Add(time.Minute)
	if _, err := store.UpsertBudgetSnapshot(ctx, got); err != nil {
		t.Fatalf("UpsertBudgetSnapshot() update error = %v", err)
	}

	updated, found, err := store.GetBudgetSnapshot(ctx, cost.ScopeExecution, "exec-rn-13-001")
	if err != nil {
		t.Fatalf("GetBudgetSnapshot() after update error = %v", err)
	}
	if !found || !updated.HardLimited() || updated.EventCount != 2 {
		t.Fatalf("updated snapshot = %+v", updated)
	}
}
