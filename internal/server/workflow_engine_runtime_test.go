package server

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/service"
	"github.com/earchibald/rein/internal/storage/sqlite"
	workflowpkg "github.com/earchibald/rein/internal/workflow"
)

const (
	runtimeTrackerAdapterID = "tracker-github-fake"
	runtimeCodingAdapterID  = "coding-copilot-fake"
	runtimeReviewAdapterID  = "review-bot-fake"
)

func TestManagedServicesWorkflowEngineFlow(t *testing.T) {
	t.Parallel()

	store, err := sqlite.OpenInMemoryAndMigrate(context.Background(), t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	catalog := newRuntimeCatalog()
	harness := newBufconnHarness(t, Options{Services: service.NewManagedSet(store, catalog)})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workflowEntity := runtimeWorkflowDefinition()
	if err := seedWorkflow(ctx, store, workflowEntity); err != nil {
		t.Fatalf("seedWorkflow() error = %v", err)
	}

	adapterClient := reinv1.NewAdapterServiceClient(harness.conn)
	workflowClient := reinv1.NewWorkflowServiceClient(harness.conn)
	projectClient := reinv1.NewProjectServiceClient(harness.conn)
	issueClient := reinv1.NewIssueServiceClient(harness.conn)
	executionClient := reinv1.NewExecutionServiceClient(harness.conn)

	adaptersResp, err := adapterClient.ListAdapters(ctx, &reinv1.ListAdaptersRequest{})
	if err != nil {
		t.Fatalf("ListAdapters() error = %v", err)
	}
	if got := runtimeAdapterIDs(adaptersResp.Adapters); !slices.Equal(got, []string{runtimeCodingAdapterID, runtimeReviewAdapterID, runtimeTrackerAdapterID}) {
		t.Fatalf("ListAdapters() ids = %v", got)
	}

	validateResp, err := workflowClient.ValidateWorkflow(ctx, &reinv1.ValidateWorkflowRequest{Workflow: workflowEntity})
	if err != nil {
		t.Fatalf("ValidateWorkflow() error = %v", err)
	}
	if !validateResp.GetValid() || len(validateResp.GetMessages()) != 0 {
		t.Fatalf("ValidateWorkflow() = %+v, want valid", validateResp)
	}

	projectResp, err := projectClient.CreateProject(ctx, &reinv1.CreateProjectRequest{Project: &reinv1.Project{
		Id:          "project-rein",
		Slug:        "rein",
		DisplayName: "rein",
		Summary:     "Workflow engine runtime test",
		Status:      reinv1.ProjectStatus_PROJECT_STATUS_ACTIVE,
	}})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}

	issueResp, err := issueClient.CreateIssue(ctx, &reinv1.CreateIssueRequest{Issue: &reinv1.Issue{
		Id:         "RN-10",
		ProjectId:  projectResp.GetProject().GetId(),
		Title:      "Workflow engine runtime test",
		Summary:    "Exercise trunk and laterals against managed services",
		Status:     reinv1.IssueStatus_ISSUE_STATUS_OPEN,
		Priority:   reinv1.IssuePriority_ISSUE_PRIORITY_HIGH,
		WorkflowId: workflowEntity.GetId(),
		Assignee:   "copilot",
	}})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	startResp, err := executionClient.StartExecution(ctx, &reinv1.StartExecutionRequest{
		IssueId:     issueResp.GetIssue().GetId(),
		WorkflowId:  workflowEntity.GetId(),
		RequestedBy: "copilot",
		Inputs:      map[string]string{"base_branch": "main"},
	})
	if err != nil {
		t.Fatalf("StartExecution() error = %v", err)
	}

	execution := startResp.GetExecution()
	if got := execution.GetStatus(); got != reinv1.ExecutionStatus_EXECUTION_STATUS_SUCCEEDED {
		t.Fatalf("StartExecution() status = %s, want %s", got, reinv1.ExecutionStatus_EXECUTION_STATUS_SUCCEEDED)
	}
	if execution.GetMetadata()["result"] != "merged" {
		t.Fatalf("StartExecution() result = %q, want merged", execution.GetMetadata()["result"])
	}

	getIssueResp, err := issueClient.GetIssue(ctx, &reinv1.GetIssueRequest{Id: issueResp.GetIssue().GetId()})
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}
	if got := getIssueResp.GetIssue().GetStatus(); got != reinv1.IssueStatus_ISSUE_STATUS_RESOLVED {
		t.Fatalf("GetIssue() status = %s, want resolved", got)
	}
	if getIssueResp.GetIssue().GetLabels()["integration_status"] != "merged" {
		t.Fatalf("GetIssue() integration_status = %q, want merged", getIssueResp.GetIssue().GetLabels()["integration_status"])
	}

	engine := workflowpkg.New(store)
	effects, err := engine.ListSideEffects(ctx, execution.GetId())
	if err != nil {
		t.Fatalf("ListSideEffects() error = %v", err)
	}
	if len(effects) != 4 {
		t.Fatalf("ListSideEffects() len = %d, want 4", len(effects))
	}
	if got := effects[1].Lane; got != "review-cycle" {
		t.Fatalf("review side effect lane = %q, want review-cycle", got)
	}
}

type runtimeCatalog struct {
	adapters map[string]*runtimeAdapter
}

func newRuntimeCatalog() *runtimeCatalog {
	return &runtimeCatalog{adapters: map[string]*runtimeAdapter{
		runtimeTrackerAdapterID: {
			descriptor: &reinv1.Adapter{Id: runtimeTrackerAdapterID, Name: "Tracker Fake", Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_TRACKER, Enabled: true},
		},
		runtimeCodingAdapterID: {
			descriptor: &reinv1.Adapter{Id: runtimeCodingAdapterID, Name: "Coding Fake", Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT, Enabled: true},
		},
		runtimeReviewAdapterID: {
			descriptor: &reinv1.Adapter{Id: runtimeReviewAdapterID, Name: "Review Fake", Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT, Enabled: true},
		},
	}}
}

func (c *runtimeCatalog) List() []*reinv1.Adapter {
	ids := make([]string, 0, len(c.adapters))
	for id := range c.adapters {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	adapters := make([]*reinv1.Adapter, 0, len(ids))
	for _, id := range ids {
		adapters = append(adapters, c.adapters[id].Descriptor())
	}
	return adapters
}

func (c *runtimeCatalog) Lookup(id string) (service.ManagedAdapter, bool) {
	adapter, ok := c.adapters[id]
	return adapter, ok
}

type runtimeAdapter struct {
	descriptor *reinv1.Adapter
}

func (a *runtimeAdapter) Descriptor() *reinv1.Adapter {
	return proto.Clone(a.descriptor).(*reinv1.Adapter)
}

func (a *runtimeAdapter) Run(_ context.Context, state *workflowpkg.RuntimeState, phase workflowpkg.Phase, direction workflowpkg.Direction, effect *workflowpkg.SideEffect) error {
	if state.Execution.Metadata == nil {
		state.Execution.Metadata = map[string]string{}
	}
	if state.Issue.Labels == nil {
		state.Issue.Labels = map[string]string{}
	}

	switch direction {
	case workflowpkg.DirectionForward:
		switch effect.Operation {
		case "prepare":
			state.Execution.Metadata["issue_url"] = fmt.Sprintf("https://tracker.fake/issues/%s", state.Issue.GetId())
			state.Execution.Metadata["branch"] = "issues/rn-10-workflow-engine-runtime-test"
			state.Execution.Metadata["worktree"] = "/worktrees/rn-10-workflow-engine-runtime-test"
			state.Issue.Labels["branch"] = state.Execution.Metadata["branch"]
			state.Issue.Labels["worktree"] = state.Execution.Metadata["worktree"]
			effect.Outputs = map[string]string{"branch": state.Execution.Metadata["branch"]}
		case "open-pr":
			if state.Execution.Metadata["branch"] == "" {
				return errors.New("branch missing before PR")
			}
			state.Execution.Metadata["pr_url"] = "https://tracker.fake/repos/rein/pull/110"
			state.Execution.Metadata["pr_state"] = "OPEN"
			state.Issue.Labels["pr_url"] = state.Execution.Metadata["pr_url"]
		case "approve-pr":
			if state.Execution.Metadata["pr_url"] == "" {
				return errors.New("pr missing before review")
			}
			state.Execution.Metadata["review_state"] = "APPROVED"
			state.Execution.Metadata["reviewed_by"] = runtimeReviewAdapterID
			state.Issue.Labels["review_state"] = "APPROVED"
		case "merge":
			if state.Execution.Metadata["review_state"] != "APPROVED" {
				return errors.New("review missing before merge")
			}
			state.Execution.Metadata["merge_commit"] = "merge-rn-10-001"
			state.Execution.Metadata["integration_branch"] = state.Execution.Metadata["base_branch"]
			state.Execution.Metadata["result"] = "merged"
			state.Issue.Labels["merge_commit"] = state.Execution.Metadata["merge_commit"]
		}
	case workflowpkg.DirectionBackward:
		switch effect.Operation {
		case "cleanup-branch":
			state.Execution.Metadata["branch_cleanup"] = "true"
		case "close-pr":
			state.Execution.Metadata["pr_state"] = "CLOSED"
		case "dismiss-review":
			state.Execution.Metadata["review_state"] = "DISMISSED"
		case "reopen-merge":
			state.Execution.Metadata["result"] = "reopened"
		}
	}
	return nil
}

func runtimeWorkflowDefinition() *reinv1.Workflow {
	created := timestamppb.New(time.Date(2026, time.May, 2, 12, 0, 0, 0, time.UTC))
	return &reinv1.Workflow{
		Id:          "managed-issue-pr-review-merge",
		Name:        "Managed issue to merge",
		Description: "Fake-backed issue to merge orchestration",
		Version:     "0.2.0",
		CreatedTime: created,
		UpdatedTime: created,
		Steps: []*reinv1.WorkflowStep{
			{Id: "prepare-branch", Name: "Prepare issue branch and worktree", AdapterId: runtimeTrackerAdapterID, Inputs: map[string]string{workflowpkg.InputOperation: "prepare", workflowpkg.InputBail: "true", workflowpkg.InputOnCancel: string(workflowpkg.CancelPolicyRewind), workflowpkg.InputBackwardOperation: "cleanup-branch"}},
			{Id: "open-pr", Name: "Open pull request", AdapterId: runtimeCodingAdapterID, Inputs: map[string]string{workflowpkg.InputLane: "review-cycle", workflowpkg.InputLaneAttach: "prepare-branch", workflowpkg.InputOperation: "open-pr", workflowpkg.InputBail: "true", workflowpkg.InputOnCancel: string(workflowpkg.CancelPolicyRewind), workflowpkg.InputBackwardOperation: "close-pr", workflowpkg.InputBackwardTo: "prepare-branch"}},
			{Id: "approve-pr", Name: "Approve pull request", AdapterId: runtimeReviewAdapterID, Inputs: map[string]string{workflowpkg.InputLane: "review-cycle", workflowpkg.InputLaneAttach: "prepare-branch", workflowpkg.InputOperation: "approve-pr", workflowpkg.InputBail: "true", workflowpkg.InputOnCancel: string(workflowpkg.CancelPolicyRewind), workflowpkg.InputBackwardOperation: "dismiss-review", workflowpkg.InputBackwardTo: "open-pr"}},
			{Id: "merge-pr", Name: "Merge pull request", AdapterId: runtimeTrackerAdapterID, Inputs: map[string]string{workflowpkg.InputOperation: "merge", workflowpkg.InputBail: "true", workflowpkg.InputBackwardOperation: "reopen-merge", workflowpkg.InputBackwardTo: "approve-pr"}},
		},
	}
}

func seedWorkflow(ctx context.Context, store *sqlite.Store, workflowEntity *reinv1.Workflow) error {
	payload, err := protojson.Marshal(workflowEntity)
	if err != nil {
		return err
	}
	_, err = store.Create(ctx, sqlite.WorkflowKind, workflowEntity.GetId(), payload)
	return err
}

func runtimeAdapterIDs(adapters []*reinv1.Adapter) []string {
	ids := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		ids = append(ids, adapter.GetId())
	}
	slices.Sort(ids)
	return ids
}
