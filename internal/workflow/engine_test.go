package workflow

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/storage/sqlite"
)

func TestCompileRejectsLateralWithoutAttach(t *testing.T) {
	t.Parallel()

	_, messages := Compile(&reinv1.Workflow{
		Id:   "wf-invalid",
		Name: "invalid",
		Steps: []*reinv1.WorkflowStep{{
			Id:        "open-pr",
			Name:      "Open PR",
			AdapterId: "coding",
			Inputs: map[string]string{
				InputLane:      "review-cycle",
				InputOperation: "open-pr",
			},
		}},
	})
	if len(messages) == 0 {
		t.Fatal("Compile() messages = 0, want validation errors")
	}
}

func TestEngineBailRewindsCompletedPhases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	engine, state := newEngineTestHarness(t, managedWorkflowDefinition())
	runner := &scriptedRunner{
		fail: map[string]error{
			"forward:merge-pr:merge": errors.New("merge failed"),
		},
	}

	err := engine.Run(ctx, state, runner)
	if err == nil || err.Error() != "merge failed" {
		t.Fatalf("Run() error = %v, want merge failed", err)
	}

	wantCalls := []string{
		"forward:prepare-branch:prepare",
		"forward:open-pr:open-pr",
		"forward:approve-pr:approve-pr",
		"forward:merge-pr:merge",
		"backward:approve-pr:dismiss-review",
		"backward:open-pr:close-pr",
		"backward:prepare-branch:cleanup-branch",
	}
	if got := runner.calls; fmt.Sprint(got) != fmt.Sprint(wantCalls) {
		t.Fatalf("Run() calls = %v, want %v", got, wantCalls)
	}

	effects, err := engine.ListSideEffects(ctx, state.Execution.GetId())
	if err != nil {
		t.Fatalf("ListSideEffects() error = %v", err)
	}
	if len(effects) != len(wantCalls) {
		t.Fatalf("ListSideEffects() len = %d, want %d", len(effects), len(wantCalls))
	}
	if got := effects[len(effects)-1].Operation; got != "cleanup-branch" {
		t.Fatalf("last side effect operation = %q, want cleanup-branch", got)
	}
}

func TestEngineRewindAndCancel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	engine, state := newEngineTestHarness(t, managedWorkflowDefinition())
	runner := &scriptedRunner{}

	if err := engine.Run(ctx, state, runner); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := engine.Rewind(ctx, state, runner, "prepare-branch", "operator rewind"); err != nil {
		t.Fatalf("Rewind() error = %v", err)
	}
	if err := engine.Cancel(ctx, state, runner, "user canceled"); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	wantCalls := []string{
		"forward:prepare-branch:prepare",
		"forward:open-pr:open-pr",
		"forward:approve-pr:approve-pr",
		"forward:merge-pr:merge",
		"backward:merge-pr:reopen-merge",
		"backward:approve-pr:dismiss-review",
		"backward:open-pr:close-pr",
		"backward:prepare-branch:cleanup-branch",
	}
	if got := runner.calls; fmt.Sprint(got) != fmt.Sprint(wantCalls) {
		t.Fatalf("calls = %v, want %v", got, wantCalls)
	}

	steps, err := engine.ListTaskSteps(ctx, state.Execution.GetId())
	if err != nil {
		t.Fatalf("ListTaskSteps() error = %v", err)
	}
	if len(steps) != len(wantCalls) {
		t.Fatalf("ListTaskSteps() len = %d, want %d", len(steps), len(wantCalls))
	}
	if got := steps[len(steps)-1].Direction; got != DirectionBackward {
		t.Fatalf("last task step direction = %s, want %s", got, DirectionBackward)
	}
}

func newEngineTestHarness(t *testing.T, workflowEntity *reinv1.Workflow) (*Engine, *RuntimeState) {
	t.Helper()

	store, err := sqlite.OpenInMemoryAndMigrate(context.Background(), t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})

	return New(store), &RuntimeState{
		Workflow: workflowEntity,
		Issue: &reinv1.Issue{
			Id:     "RN-10",
			Labels: map[string]string{},
		},
		Execution: &reinv1.Execution{
			Id:       "exec-rn-10-001",
			IssueId:  "RN-10",
			Metadata: map[string]string{"base_branch": "main"},
		},
	}
}

type scriptedRunner struct {
	calls []string
	fail  map[string]error
}

func (r *scriptedRunner) Run(_ context.Context, state *RuntimeState, phase Phase, direction Direction, effect *SideEffect) error {
	key := fmt.Sprintf("%s:%s:%s", direction, phase.ID, effect.Operation)
	r.calls = append(r.calls, key)
	if state.Execution.Metadata == nil {
		state.Execution.Metadata = map[string]string{}
	}
	if state.Issue.Labels == nil {
		state.Issue.Labels = map[string]string{}
	}
	state.Execution.Metadata[phase.ID] = string(direction)
	state.Issue.Labels[phase.ID] = effect.Operation
	effect.Outputs = map[string]string{"phase": phase.ID, "direction": string(direction)}
	if err, ok := r.fail[key]; ok {
		return err
	}
	return nil
}

func managedWorkflowDefinition() *reinv1.Workflow {
	created := timestamppb.New(time.Date(2026, time.May, 2, 12, 0, 0, 0, time.UTC))
	return &reinv1.Workflow{
		Id:          "managed-issue-pr-review-merge",
		Name:        "Managed issue to merge",
		Description: "Trunk plus review lateral workflow",
		Version:     "0.2.0",
		CreatedTime: created,
		UpdatedTime: created,
		Steps: []*reinv1.WorkflowStep{
			{
				Id:        "prepare-branch",
				Name:      "Prepare issue branch and worktree",
				AdapterId: "tracker",
				Inputs: map[string]string{
					InputOperation:         "prepare",
					InputBail:              "true",
					InputOnCancel:          string(CancelPolicyRewind),
					InputBackwardOperation: "cleanup-branch",
				},
			},
			{
				Id:        "open-pr",
				Name:      "Open pull request",
				AdapterId: "coding",
				Inputs: map[string]string{
					InputLane:              "review-cycle",
					InputLaneAttach:        "prepare-branch",
					InputOperation:         "open-pr",
					InputBail:              "true",
					InputOnCancel:          string(CancelPolicyRewind),
					InputBackwardOperation: "close-pr",
					InputBackwardTo:        "prepare-branch",
				},
			},
			{
				Id:        "approve-pr",
				Name:      "Approve pull request",
				AdapterId: "review",
				Inputs: map[string]string{
					InputLane:              "review-cycle",
					InputLaneAttach:        "prepare-branch",
					InputOperation:         "approve-pr",
					InputBail:              "true",
					InputOnCancel:          string(CancelPolicyRewind),
					InputBackwardOperation: "dismiss-review",
					InputBackwardTo:        "open-pr",
				},
			},
			{
				Id:        "merge-pr",
				Name:      "Merge pull request",
				AdapterId: "tracker",
				Inputs: map[string]string{
					InputOperation:         "merge",
					InputBail:              "true",
					InputOnCancel:          string(CancelPolicyBail),
					InputBackwardOperation: "reopen-merge",
					InputBackwardTo:        "approve-pr",
				},
			},
		},
	}
}
