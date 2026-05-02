package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/earchibald/rein/adaptertest"
	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/cost"
	"github.com/earchibald/rein/internal/credentials"
	"github.com/earchibald/rein/internal/service"
	"github.com/earchibald/rein/internal/storage/sqlite"
)

const (
	fakeTrackerAdapterID = "tracker-github-fake"
	fakeCodingAdapterID  = "coding-copilot-fake"
	fakeReviewAdapterID  = "review-bot-fake"
)

func TestManagedFlowHarnessIssueToMergeFlow(t *testing.T) {
	t.Parallel()

	harness := newManagedFlowHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	costEventsCh, unsubscribe := harness.subscribeCostEvents(4)
	defer unsubscribe()

	workflow := managedFlowWorkflow()
	if err := harness.seedWorkflow(ctx, workflow); err != nil {
		t.Fatalf("seedWorkflow() error = %v", err)
	}

	adaptersResp, err := harness.adapterClient.ListAdapters(ctx, &reinv1.ListAdaptersRequest{})
	if err != nil {
		t.Fatalf("ListAdapters() error = %v", err)
	}
	if got := adapterIDs(adaptersResp.Adapters); !slices.Equal(got, []string{fakeCodingAdapterID, fakeReviewAdapterID, fakeTrackerAdapterID}) {
		t.Fatalf("ListAdapters() ids = %v", got)
	}

	workflowResp, err := harness.workflowClient.GetWorkflow(ctx, &reinv1.GetWorkflowRequest{Id: workflow.GetId()})
	if err != nil {
		t.Fatalf("GetWorkflow() error = %v", err)
	}
	if !proto.Equal(workflowResp.GetWorkflow(), workflow) {
		t.Fatalf("GetWorkflow() workflow = %v, want %v", workflowResp.GetWorkflow(), workflow)
	}

	validateResp, err := harness.workflowClient.ValidateWorkflow(ctx, &reinv1.ValidateWorkflowRequest{Workflow: workflow})
	if err != nil {
		t.Fatalf("ValidateWorkflow() error = %v", err)
	}
	if !validateResp.GetValid() || len(validateResp.GetMessages()) != 0 {
		t.Fatalf("ValidateWorkflow() = %+v, want valid with no messages", validateResp)
	}

	projectResp, err := harness.projectClient.CreateProject(ctx, &reinv1.CreateProjectRequest{
		Project: &reinv1.Project{
			Id:          "project-rein",
			Slug:        "rein",
			DisplayName: "rein",
			Summary:     "Managed flow harness",
			Status:      reinv1.ProjectStatus_PROJECT_STATUS_ACTIVE,
		},
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if projectResp.GetProject().GetCreatedTime() == nil || projectResp.GetProject().GetUpdatedTime() == nil {
		t.Fatalf("CreateProject() timestamps missing: %+v", projectResp.GetProject())
	}

	getProjectResp, err := harness.projectClient.GetProject(ctx, &reinv1.GetProjectRequest{Id: projectResp.GetProject().GetId()})
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	if !proto.Equal(getProjectResp.GetProject(), projectResp.GetProject()) {
		t.Fatalf("GetProject() project = %v, want %v", getProjectResp.GetProject(), projectResp.GetProject())
	}

	issueResp, err := harness.issueClient.CreateIssue(ctx, &reinv1.CreateIssueRequest{
		Issue: &reinv1.Issue{
			Id:         "RN-9",
			ProjectId:  projectResp.GetProject().GetId(),
			Title:      "E2E flow harness against fakes",
			Summary:    "Exercise the managed issue to merge flow against fake adapters",
			Status:     reinv1.IssueStatus_ISSUE_STATUS_OPEN,
			Priority:   reinv1.IssuePriority_ISSUE_PRIORITY_HIGH,
			WorkflowId: workflow.GetId(),
			Assignee:   "copilot",
		},
	})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if got := issueResp.GetIssue().GetStatus(); got != reinv1.IssueStatus_ISSUE_STATUS_OPEN {
		t.Fatalf("CreateIssue() status = %s, want %s", got, reinv1.IssueStatus_ISSUE_STATUS_OPEN)
	}

	startResp, err := harness.executionClient.StartExecution(ctx, &reinv1.StartExecutionRequest{
		IssueId:     issueResp.GetIssue().GetId(),
		WorkflowId:  workflow.GetId(),
		RequestedBy: "copilot",
		Inputs: map[string]string{
			"base_branch": "main",
		},
	})
	if err != nil {
		t.Fatalf("StartExecution() error = %v", err)
	}

	execution := startResp.GetExecution()
	if got := execution.GetStatus(); got != reinv1.ExecutionStatus_EXECUTION_STATUS_SUCCEEDED {
		t.Fatalf("StartExecution() status = %s, want %s", got, reinv1.ExecutionStatus_EXECUTION_STATUS_SUCCEEDED)
	}
	if execution.GetStartedTime() == nil || execution.GetFinishedTime() == nil {
		t.Fatalf("StartExecution() timestamps missing: %+v", execution)
	}
	if execution.GetFinishedTime().AsTime().Before(execution.GetStartedTime().AsTime()) {
		t.Fatalf("StartExecution() finished_time = %s before started_time = %s", execution.GetFinishedTime().AsTime(), execution.GetStartedTime().AsTime())
	}

	wantMetadata := map[string]string{
		"base_branch":        "main",
		"issue_url":          "https://tracker.fake/issues/RN-9",
		"branch":             "issues/rn-9-e2e-flow-harness-against-fakes",
		"worktree":           "/worktrees/rn-9-e2e-flow-harness-against-fakes",
		"pr_url":             "https://tracker.fake/repos/rein/pull/101",
		"pr_state":           "OPEN",
		"review_state":       "APPROVED",
		"reviewed_by":        fakeReviewAdapterID,
		"merge_commit":       "merge-rn-9-001",
		"integration_branch": "main",
		"result":             "merged",
	}
	for key, want := range wantMetadata {
		if got := execution.GetMetadata()[key]; got != want {
			t.Fatalf("StartExecution() metadata[%q] = %q, want %q", key, got, want)
		}
	}

	getExecutionResp, err := harness.executionClient.GetExecution(ctx, &reinv1.GetExecutionRequest{Id: execution.GetId()})
	if err != nil {
		t.Fatalf("GetExecution() error = %v", err)
	}
	if !proto.Equal(getExecutionResp.GetExecution(), execution) {
		t.Fatalf("GetExecution() execution = %v, want %v", getExecutionResp.GetExecution(), execution)
	}

	getIssueResp, err := harness.issueClient.GetIssue(ctx, &reinv1.GetIssueRequest{Id: issueResp.GetIssue().GetId()})
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}

	issue := getIssueResp.GetIssue()
	if got := issue.GetStatus(); got != reinv1.IssueStatus_ISSUE_STATUS_RESOLVED {
		t.Fatalf("GetIssue() status = %s, want %s", got, reinv1.IssueStatus_ISSUE_STATUS_RESOLVED)
	}
	if !issue.GetUpdatedTime().AsTime().After(issueResp.GetIssue().GetUpdatedTime().AsTime()) {
		t.Fatalf("GetIssue() updated_time = %s, want after %s", issue.GetUpdatedTime().AsTime(), issueResp.GetIssue().GetUpdatedTime().AsTime())
	}

	wantLabels := map[string]string{
		"branch":             "issues/rn-9-e2e-flow-harness-against-fakes",
		"worktree":           "/worktrees/rn-9-e2e-flow-harness-against-fakes",
		"pr_url":             "https://tracker.fake/repos/rein/pull/101",
		"review_state":       "APPROVED",
		"merge_commit":       "merge-rn-9-001",
		"integration_status": "merged",
	}
	for key, want := range wantLabels {
		if got := issue.GetLabels()[key]; got != want {
			t.Fatalf("GetIssue() labels[%q] = %q, want %q", key, got, want)
		}
	}

	streamedCostEvents := collectCostEvents(t, ctx, costEventsCh, 4)
	if got := []string{
		streamedCostEvents[0].AdapterID,
		streamedCostEvents[1].AdapterID,
		streamedCostEvents[2].AdapterID,
		streamedCostEvents[3].AdapterID,
	}; !slices.Equal(got, []string{fakeTrackerAdapterID, fakeCodingAdapterID, fakeReviewAdapterID, fakeTrackerAdapterID}) {
		t.Fatalf("streamed adapter ids = %v", got)
	}

	storedCostEvents, err := harness.listCostEvents(ctx, execution.GetId())
	if err != nil {
		t.Fatalf("listCostEvents() error = %v", err)
	}
	if len(storedCostEvents) != 4 {
		t.Fatalf("listCostEvents() count = %d, want 4", len(storedCostEvents))
	}

	executionBudget, found, err := harness.budgetSnapshot(ctx, cost.ScopeExecution, execution.GetId())
	if err != nil {
		t.Fatalf("budgetSnapshot() error = %v", err)
	}
	if !found {
		t.Fatal("budgetSnapshot() found = false, want true")
	}
	if executionBudget.EventCount != 4 || executionBudget.SpentMicros != 5600 {
		t.Fatalf("execution budget snapshot = %+v, want 4 events and 5600 micros", executionBudget)
	}
}

func TestManagedFlowHarnessBudgetHardLimitBlocksNewLaunches(t *testing.T) {
	t.Parallel()

	harness := newManagedFlowHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	costEventsCh, unsubscribe := harness.subscribeCostEvents(1)
	defer unsubscribe()

	workflow := managedFlowWorkflow()
	if err := harness.seedWorkflow(ctx, workflow); err != nil {
		t.Fatalf("seedWorkflow() error = %v", err)
	}

	projectResp, err := harness.projectClient.CreateProject(ctx, &reinv1.CreateProjectRequest{
		Project: &reinv1.Project{
			Id:          "project-rein",
			Slug:        "rein",
			DisplayName: "rein",
			Summary:     "Managed flow harness",
			Status:      reinv1.ProjectStatus_PROJECT_STATUS_ACTIVE,
		},
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}

	issueResp, err := harness.issueClient.CreateIssue(ctx, &reinv1.CreateIssueRequest{
		Issue: &reinv1.Issue{
			Id:         "RN-13",
			ProjectId:  projectResp.GetProject().GetId(),
			Title:      "Cost telemetry budgets",
			Summary:    "Exercise launch blocking after a hard budget hit",
			Status:     reinv1.IssueStatus_ISSUE_STATUS_OPEN,
			Priority:   reinv1.IssuePriority_ISSUE_PRIORITY_HIGH,
			WorkflowId: workflow.GetId(),
			Assignee:   "copilot",
		},
	})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	if _, err := harness.configureBudget(ctx, cost.ScopeExecution, "exec-rn-9-001", cost.Limits{HardLimitMicros: 1000}); err != nil {
		t.Fatalf("configureBudget() error = %v", err)
	}

	_, err = harness.executionClient.StartExecution(ctx, &reinv1.StartExecutionRequest{
		IssueId:     issueResp.GetIssue().GetId(),
		WorkflowId:  workflow.GetId(),
		RequestedBy: "copilot",
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("StartExecution() status = %v, want %v", status.Code(err), codes.ResourceExhausted)
	}

	streamedCostEvents := collectCostEvents(t, ctx, costEventsCh, 1)
	if len(streamedCostEvents) != 1 || streamedCostEvents[0].AdapterID != fakeTrackerAdapterID {
		t.Fatalf("streamedCostEvents = %+v", streamedCostEvents)
	}

	executionResp, err := harness.executionClient.GetExecution(ctx, &reinv1.GetExecutionRequest{Id: "exec-rn-9-001"})
	if err != nil {
		t.Fatalf("GetExecution() error = %v", err)
	}
	execution := executionResp.GetExecution()
	if execution.GetStatus() != reinv1.ExecutionStatus_EXECUTION_STATUS_FAILED {
		t.Fatalf("GetExecution() status = %s, want %s", execution.GetStatus(), reinv1.ExecutionStatus_EXECUTION_STATUS_FAILED)
	}
	if execution.GetMetadata()["result"] != "budget_hard_limit" {
		t.Fatalf("GetExecution() result = %q, want %q", execution.GetMetadata()["result"], "budget_hard_limit")
	}
	if execution.GetMetadata()["branch"] == "" || execution.GetMetadata()["worktree"] == "" {
		t.Fatalf("GetExecution() missing first-step metadata: %+v", execution.GetMetadata())
	}
	if execution.GetMetadata()["pr_url"] != "" {
		t.Fatalf("GetExecution() pr_url = %q, want empty", execution.GetMetadata()["pr_url"])
	}

	issueState, err := harness.issueClient.GetIssue(ctx, &reinv1.GetIssueRequest{Id: issueResp.GetIssue().GetId()})
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}
	if issueState.GetIssue().GetStatus() != reinv1.IssueStatus_ISSUE_STATUS_IN_PROGRESS {
		t.Fatalf("GetIssue() status = %s, want %s", issueState.GetIssue().GetStatus(), reinv1.IssueStatus_ISSUE_STATUS_IN_PROGRESS)
	}

	storedCostEvents, err := harness.listCostEvents(ctx, execution.GetId())
	if err != nil {
		t.Fatalf("listCostEvents() error = %v", err)
	}
	if len(storedCostEvents) != 1 {
		t.Fatalf("listCostEvents() count = %d, want 1", len(storedCostEvents))
	}

	executionBudget, found, err := harness.budgetSnapshot(ctx, cost.ScopeExecution, execution.GetId())
	if err != nil {
		t.Fatalf("budgetSnapshot() error = %v", err)
	}
	if !found || !executionBudget.HardLimited() || executionBudget.EventCount != 1 || executionBudget.SpentMicros != 1200 {
		t.Fatalf("execution budget snapshot = %+v, want hard-limited first-step spend", executionBudget)
	}
}

func TestManagedFlowHarnessStoresCredentialReferencesNotValues(t *testing.T) {
	t.Parallel()

	harness := newManagedFlowHarness(t)
	harness.executionServer.lookupEnv = func(name string) (string, bool) {
		if name != "RN15_GITHUB_TOKEN" {
			return "", false
		}
		return "super-secret-token", true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workflow := managedFlowWorkflow()
	if err := harness.seedWorkflow(ctx, workflow); err != nil {
		t.Fatalf("seedWorkflow() error = %v", err)
	}

	projectResp, err := harness.projectClient.CreateProject(ctx, &reinv1.CreateProjectRequest{
		Project: &reinv1.Project{
			Id:          "project-rein",
			Slug:        "rein",
			DisplayName: "rein",
			Summary:     "Managed flow harness",
			Status:      reinv1.ProjectStatus_PROJECT_STATUS_ACTIVE,
		},
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}

	issueResp, err := harness.issueClient.CreateIssue(ctx, &reinv1.CreateIssueRequest{
		Issue: &reinv1.Issue{
			Id:         "RN-15",
			ProjectId:  projectResp.GetProject().GetId(),
			Title:      "Credential providers",
			Summary:    "Ensure execution credentials resolve without persisting values",
			Status:     reinv1.IssueStatus_ISSUE_STATUS_OPEN,
			Priority:   reinv1.IssuePriority_ISSUE_PRIORITY_HIGH,
			WorkflowId: workflow.GetId(),
			Assignee:   "copilot",
		},
	})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	startResp, err := harness.executionClient.StartExecution(ctx, &reinv1.StartExecutionRequest{
		IssueId:     issueResp.GetIssue().GetId(),
		WorkflowId:  workflow.GetId(),
		RequestedBy: "copilot",
		Inputs: map[string]string{
			"base_branch":                 "main",
			"credential_ref.github_token": "env://RN15_GITHUB_TOKEN",
		},
	})
	if err != nil {
		t.Fatalf("StartExecution() error = %v", err)
	}

	execution := startResp.GetExecution()
	if got := execution.GetMetadata()["credential_ref.github_token"]; got != "env://RN15_GITHUB_TOKEN" {
		t.Fatalf("StartExecution() credential ref = %q, want opaque env ref", got)
	}
	if got := execution.GetMetadata()["auth_mode"]; got != "credential" {
		t.Fatalf("StartExecution() auth_mode = %q, want credential", got)
	}
	for key, value := range execution.GetMetadata() {
		if value == "super-secret-token" {
			t.Fatalf("StartExecution() metadata[%q] leaked secret value", key)
		}
	}
}

type managedFlowHarness struct {
	adapterClient   reinv1.AdapterServiceClient
	costRecorder    *cost.Recorder
	costStream      *cost.Stream
	executionClient reinv1.ExecutionServiceClient
	executionServer *managedFlowExecutionService
	issueClient     reinv1.IssueServiceClient
	projectClient   reinv1.ProjectServiceClient
	workflowClient  reinv1.WorkflowServiceClient
	store           *sqlite.Store
}

func newManagedFlowHarness(tb testing.TB) *managedFlowHarness {
	tb.Helper()

	store, err := sqlite.OpenInMemoryAndMigrate(context.Background(), fmt.Sprintf("%s-managed-flow", tb.Name()))
	if err != nil {
		tb.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	tb.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			tb.Errorf("Close() error = %v", closeErr)
		}
	})

	adapters := newManagedFlowAdapterCatalog(tb)
	costStream := cost.NewStream()
	costRecorder := cost.NewRecorder(store, cost.WithPublisher(costStream))
	executionServer := &managedFlowExecutionService{
		adapters:           adapters,
		costRecorder:       costRecorder,
		credentialRegistry: credentials.NewBuiltinRegistry(credentials.BuiltinOptions{}),
		lookupEnv:          os.LookupEnv,
		store:              store,
	}
	bufconn := newBufconnHarness(tb, Options{
		Services: service.Set{
			Adapter:   &managedFlowAdapterService{catalog: adapters},
			Execution: executionServer,
			Issue:     &managedFlowIssueService{store: store},
			Project:   &managedFlowProjectService{store: store},
			Workflow:  &managedFlowWorkflowService{catalog: adapters, store: store},
		},
	})

	return &managedFlowHarness{
		adapterClient:   reinv1.NewAdapterServiceClient(bufconn.conn),
		costRecorder:    costRecorder,
		costStream:      costStream,
		executionClient: reinv1.NewExecutionServiceClient(bufconn.conn),
		executionServer: executionServer,
		issueClient:     reinv1.NewIssueServiceClient(bufconn.conn),
		projectClient:   reinv1.NewProjectServiceClient(bufconn.conn),
		workflowClient:  reinv1.NewWorkflowServiceClient(bufconn.conn),
		store:           store,
	}
}

func (h *managedFlowHarness) seedWorkflow(ctx context.Context, workflow *reinv1.Workflow) error {
	_, err := createStoredProto(ctx, h.store, sqlite.WorkflowKind, workflow.GetId(), workflow)
	return err
}

func (h *managedFlowHarness) subscribeCostEvents(buffer int) (events <-chan cost.Event, unsubscribe func()) {
	return h.costStream.Subscribe(buffer)
}

func (h *managedFlowHarness) configureBudget(ctx context.Context, scope cost.Scope, scopeID string, limits cost.Limits) (cost.Snapshot, error) {
	return h.costRecorder.ConfigureBudget(ctx, scope, scopeID, "USD", limits)
}

func (h *managedFlowHarness) listCostEvents(ctx context.Context, executionID string) ([]cost.Event, error) {
	return h.store.ListCostEvents(ctx, sqlite.CostEventFilter{ExecutionID: executionID})
}

func (h *managedFlowHarness) budgetSnapshot(ctx context.Context, scope cost.Scope, scopeID string) (cost.Snapshot, bool, error) {
	return h.store.GetBudgetSnapshot(ctx, scope, scopeID)
}

type managedFlowAdapterService struct {
	reinv1.UnimplementedAdapterServiceServer
	catalog *managedFlowAdapterCatalog
}

func (s *managedFlowAdapterService) ListAdapters(context.Context, *reinv1.ListAdaptersRequest) (*reinv1.ListAdaptersResponse, error) {
	return &reinv1.ListAdaptersResponse{Adapters: s.catalog.List()}, nil
}

func (s *managedFlowAdapterService) GetAdapter(_ context.Context, req *reinv1.GetAdapterRequest) (*reinv1.GetAdapterResponse, error) {
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	adapter, ok := s.catalog.Get(req.GetId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "adapter %q not found", req.GetId())
	}

	return &reinv1.GetAdapterResponse{Adapter: adapter}, nil
}

type managedFlowWorkflowService struct {
	reinv1.UnimplementedWorkflowServiceServer
	catalog *managedFlowAdapterCatalog
	store   *sqlite.Store
}

func (s *managedFlowWorkflowService) GetWorkflow(ctx context.Context, req *reinv1.GetWorkflowRequest) (*reinv1.GetWorkflowResponse, error) {
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	workflow, _, err := loadStoredWorkflow(ctx, s.store, req.GetId())
	if err != nil {
		return nil, toStatusError("workflow", req.GetId(), err)
	}

	return &reinv1.GetWorkflowResponse{Workflow: workflow}, nil
}

func (s *managedFlowWorkflowService) ValidateWorkflow(_ context.Context, req *reinv1.ValidateWorkflowRequest) (*reinv1.ValidateWorkflowResponse, error) {
	if req.GetWorkflow() == nil {
		return nil, status.Error(codes.InvalidArgument, "workflow is required")
	}

	messages := validateManagedFlowWorkflow(req.GetWorkflow(), s.catalog)
	return &reinv1.ValidateWorkflowResponse{
		Valid:    len(messages) == 0,
		Messages: messages,
	}, nil
}

type managedFlowProjectService struct {
	reinv1.UnimplementedProjectServiceServer
	store *sqlite.Store
}

func (s *managedFlowProjectService) CreateProject(ctx context.Context, req *reinv1.CreateProjectRequest) (*reinv1.CreateProjectResponse, error) {
	if req.GetProject() == nil {
		return nil, status.Error(codes.InvalidArgument, "project is required")
	}

	project := proto.Clone(req.GetProject()).(*reinv1.Project)
	if strings.TrimSpace(project.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "project.id is required")
	}
	if project.GetStatus() == reinv1.ProjectStatus_PROJECT_STATUS_UNSPECIFIED {
		project.Status = reinv1.ProjectStatus_PROJECT_STATUS_ACTIVE
	}
	if project.Labels == nil {
		project.Labels = map[string]string{}
	}

	now := timestamppb.Now()
	if project.CreatedTime == nil {
		project.CreatedTime = now
	}
	project.UpdatedTime = now

	if _, err := createStoredProto(ctx, s.store, sqlite.ProjectKind, project.GetId(), project); err != nil {
		return nil, status.Errorf(codes.Internal, "create project %q: %v", project.GetId(), err)
	}

	return &reinv1.CreateProjectResponse{Project: project}, nil
}

func (s *managedFlowProjectService) GetProject(ctx context.Context, req *reinv1.GetProjectRequest) (*reinv1.GetProjectResponse, error) {
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	project, _, err := loadStoredProject(ctx, s.store, req.GetId())
	if err != nil {
		return nil, toStatusError("project", req.GetId(), err)
	}

	return &reinv1.GetProjectResponse{Project: project}, nil
}

type managedFlowIssueService struct {
	reinv1.UnimplementedIssueServiceServer
	store *sqlite.Store
}

func (s *managedFlowIssueService) CreateIssue(ctx context.Context, req *reinv1.CreateIssueRequest) (*reinv1.CreateIssueResponse, error) {
	if req.GetIssue() == nil {
		return nil, status.Error(codes.InvalidArgument, "issue is required")
	}

	issue := proto.Clone(req.GetIssue()).(*reinv1.Issue)
	if strings.TrimSpace(issue.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "issue.id is required")
	}
	if strings.TrimSpace(issue.GetProjectId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "issue.project_id is required")
	}
	if issue.GetStatus() == reinv1.IssueStatus_ISSUE_STATUS_UNSPECIFIED {
		issue.Status = reinv1.IssueStatus_ISSUE_STATUS_OPEN
	}
	if issue.Labels == nil {
		issue.Labels = map[string]string{}
	}

	now := timestamppb.Now()
	if issue.CreatedTime == nil {
		issue.CreatedTime = now
	}
	issue.UpdatedTime = now

	if _, err := createStoredProto(ctx, s.store, sqlite.IssueKind, issue.GetId(), issue); err != nil {
		return nil, status.Errorf(codes.Internal, "create issue %q: %v", issue.GetId(), err)
	}

	return &reinv1.CreateIssueResponse{Issue: issue}, nil
}

func (s *managedFlowIssueService) GetIssue(ctx context.Context, req *reinv1.GetIssueRequest) (*reinv1.GetIssueResponse, error) {
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	issue, _, err := loadStoredIssue(ctx, s.store, req.GetId())
	if err != nil {
		return nil, toStatusError("issue", req.GetId(), err)
	}

	return &reinv1.GetIssueResponse{Issue: issue}, nil
}

type managedFlowExecutionService struct {
	reinv1.UnimplementedExecutionServiceServer
	adapters           *managedFlowAdapterCatalog
	costRecorder       *cost.Recorder
	credentialRegistry *credentials.Registry
	lookupEnv          func(string) (string, bool)
	store              *sqlite.Store
}

func (s *managedFlowExecutionService) StartExecution(ctx context.Context, req *reinv1.StartExecutionRequest) (*reinv1.StartExecutionResponse, error) {
	if strings.TrimSpace(req.GetIssueId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "issue_id is required")
	}
	if strings.TrimSpace(req.GetWorkflowId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "workflow_id is required")
	}

	issue, issueRecord, err := loadStoredIssue(ctx, s.store, req.GetIssueId())
	if err != nil {
		return nil, toStatusError("issue", req.GetIssueId(), err)
	}
	workflow, _, err := loadStoredWorkflow(ctx, s.store, req.GetWorkflowId())
	if err != nil {
		return nil, toStatusError("workflow", req.GetWorkflowId(), err)
	}

	if messages := validateManagedFlowWorkflow(workflow, s.adapters); len(messages) > 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "workflow %q is invalid", workflow.GetId())
	}

	now := timestamppb.Now()
	issue.Status = reinv1.IssueStatus_ISSUE_STATUS_IN_PROGRESS
	issue.UpdatedTime = now
	if issue.Labels == nil {
		issue.Labels = map[string]string{}
	}
	issue.Labels["integration_status"] = "running"

	issueRecord, err = updateStoredProto(ctx, s.store, sqlite.IssueKind, issue.GetId(), issueRecord.LockVersion, issue)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "update issue %q: %v", issue.GetId(), err)
	}

	execution := &reinv1.Execution{
		Id:          "exec-rn-9-001",
		IssueId:     issue.GetId(),
		WorkflowId:  workflow.GetId(),
		AdapterId:   workflow.GetSteps()[0].GetAdapterId(),
		Status:      reinv1.ExecutionStatus_EXECUTION_STATUS_QUEUED,
		RequestedBy: req.GetRequestedBy(),
		CreatedTime: now,
		Metadata:    cloneStringMap(req.GetInputs()),
	}
	if execution.Metadata == nil {
		execution.Metadata = map[string]string{}
	}
	if execution.Metadata["base_branch"] == "" {
		execution.Metadata["base_branch"] = "main"
	}

	executionRecord, err := createStoredProto(ctx, s.store, sqlite.ExecutionKind, execution.GetId(), execution)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create execution %q: %v", execution.GetId(), err)
	}

	execution.Status = reinv1.ExecutionStatus_EXECUTION_STATUS_RUNNING
	execution.StartedTime = now
	executionRecord, err = updateStoredProto(ctx, s.store, sqlite.ExecutionKind, execution.GetId(), executionRecord.LockVersion, execution)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "start execution %q: %v", execution.GetId(), err)
	}

	resolvedCredentials, err := s.resolveCredentials(ctx, issue, execution)
	if err != nil {
		execution.Status = reinv1.ExecutionStatus_EXECUTION_STATUS_FAILED
		execution.FinishedTime = timestamppb.Now()
		execution.Metadata["result"] = "failed"
		if _, updateErr := updateStoredProto(ctx, s.store, sqlite.ExecutionKind, execution.GetId(), executionRecord.LockVersion, execution); updateErr != nil {
			return nil, status.Errorf(codes.Internal, "execution %q credential resolution failed with %v and could not be stored: %v", execution.GetId(), err, updateErr)
		}
		return nil, status.Errorf(codes.FailedPrecondition, "resolve execution credentials: %v", err)
	}

	state := &managedFlowState{
		costRecorder: s.costRecorder,
		credentials:  resolvedCredentials,
		execution:    execution,
		issue:        issue,
		workflow:     workflow,
	}

	for _, step := range workflow.GetSteps() {
		if s.costRecorder != nil {
			admission, err := s.costRecorder.CheckLaunch(ctx, cost.ScopePath{
				ProjectID:   issue.GetProjectId(),
				IssueID:     issue.GetId(),
				ExecutionID: execution.GetId(),
			})
			if err != nil {
				return nil, status.Errorf(codes.Internal, "check budgets for execution %q: %v", execution.GetId(), err)
			}
			if !admission.Allowed {
				execution.Status = reinv1.ExecutionStatus_EXECUTION_STATUS_FAILED
				execution.FinishedTime = timestamppb.Now()
				execution.Metadata["budget_block_scope"] = string(admission.BlockedBy.Scope)
				execution.Metadata["budget_block_scope_id"] = admission.BlockedBy.ScopeID
				execution.Metadata["result"] = "budget_hard_limit"
				if _, updateErr := updateStoredProto(ctx, s.store, sqlite.ExecutionKind, execution.GetId(), executionRecord.LockVersion, execution); updateErr != nil {
					return nil, status.Errorf(codes.Internal, "store budget-blocked execution %q: %v", execution.GetId(), updateErr)
				}
				return nil, status.Errorf(codes.ResourceExhausted, "budget hard limit reached for %s %q", admission.BlockedBy.Scope, admission.BlockedBy.ScopeID)
			}
			if len(admission.Warnings) > 0 {
				execution.Metadata["budget_soft_warning_scope"] = string(admission.Warnings[0].Scope)
			}
		}

		err := s.adapters.Run(ctx, state, step)
		if err == nil {
			continue
		}

		execution.Status = reinv1.ExecutionStatus_EXECUTION_STATUS_FAILED
		execution.FinishedTime = timestamppb.Now()
		execution.Metadata["result"] = "failed"
		if _, updateErr := updateStoredProto(ctx, s.store, sqlite.ExecutionKind, execution.GetId(), executionRecord.LockVersion, execution); updateErr != nil {
			return nil, status.Errorf(codes.Internal, "execution %q failed with %v and could not be stored: %v", execution.GetId(), err, updateErr)
		}
		return nil, status.Errorf(codes.FailedPrecondition, "run workflow %q: %v", workflow.GetId(), err)
	}

	issue.Status = reinv1.IssueStatus_ISSUE_STATUS_RESOLVED
	issue.UpdatedTime = timestamppb.Now()
	issue.Labels["integration_status"] = "merged"
	if _, err := updateStoredProto(ctx, s.store, sqlite.IssueKind, issue.GetId(), issueRecord.LockVersion, issue); err != nil {
		return nil, status.Errorf(codes.Internal, "resolve issue %q: %v", issue.GetId(), err)
	}

	execution.Status = reinv1.ExecutionStatus_EXECUTION_STATUS_SUCCEEDED
	execution.FinishedTime = timestamppb.Now()
	execution.Metadata["result"] = "merged"
	if _, err := updateStoredProto(ctx, s.store, sqlite.ExecutionKind, execution.GetId(), executionRecord.LockVersion, execution); err != nil {
		return nil, status.Errorf(codes.Internal, "finish execution %q: %v", execution.GetId(), err)
	}

	storedExecution, _, err := loadStoredExecution(ctx, s.store, execution.GetId())
	if err != nil {
		return nil, toStatusError("execution", execution.GetId(), err)
	}

	return &reinv1.StartExecutionResponse{Execution: storedExecution}, nil
}

func (s *managedFlowExecutionService) GetExecution(ctx context.Context, req *reinv1.GetExecutionRequest) (*reinv1.GetExecutionResponse, error) {
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	execution, _, err := loadStoredExecution(ctx, s.store, req.GetId())
	if err != nil {
		return nil, toStatusError("execution", req.GetId(), err)
	}

	return &reinv1.GetExecutionResponse{Execution: execution}, nil
}

type managedFlowState struct {
	costRecorder *cost.Recorder
	credentials  map[string]string
	execution    *reinv1.Execution
	issue        *reinv1.Issue
	workflow     *reinv1.Workflow
}

func (s *managedFlowExecutionService) resolveCredentials(ctx context.Context, issue *reinv1.Issue, execution *reinv1.Execution) (map[string]string, error) {
	if s.credentialRegistry == nil {
		return nil, nil
	}

	references := map[string]string{}
	for key, value := range execution.GetMetadata() {
		name, ok := strings.CutPrefix(key, "credential_ref.")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		references[name] = value
	}

	return s.credentialRegistry.ResolveAll(ctx, references, credentials.ExecutionScope{
		ProjectID:   issue.GetProjectId(),
		WorkflowID:  execution.GetWorkflowId(),
		ExecutionID: execution.GetId(),
		LookupEnv:   s.lookupEnv,
	})
}

type managedFlowAdapterCatalog struct {
	adapters map[string]managedFlowAdapter
}

func newManagedFlowAdapterCatalog(tb testing.TB) *managedFlowAdapterCatalog {
	tb.Helper()

	tracker := &managedFlowTrackerAdapter{
		descriptor: &reinv1.Adapter{
			Id:          fakeTrackerAdapterID,
			Name:        "GitHub Tracker Fake",
			Category:    reinv1.AdapterCategory_ADAPTER_CATEGORY_TRACKER,
			Description: "Simulates issue, branch, worktree, PR, and merge coordination",
			Version:     "0.1.0",
			Enabled:     true,
			Capabilities: map[string]string{
				"issue.sync":      "true",
				"branch.prepare":  "true",
				"worktree.create": "true",
				"pull_request":    "true",
				"merge":           "true",
			},
		},
	}
	adaptertest.RunTracker(tb, adaptertest.Spec{
		Descriptor:           tracker.Descriptor(),
		Implementation:       tracker,
		Contract:             (*managedFlowTrackerContract)(nil),
		RequiredCapabilities: []string{"issue.sync", "branch.prepare", "worktree.create", "pull_request", "merge"},
	})

	coding := &managedFlowCodingAdapter{
		descriptor: &reinv1.Adapter{
			Id:          fakeCodingAdapterID,
			Name:        "Copilot Fake",
			Category:    reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
			Description: "Simulates code generation and PR drafting",
			Version:     "0.1.0",
			Enabled:     true,
			Capabilities: map[string]string{
				"patch.apply":  "true",
				"pull_request": "true",
			},
		},
	}
	adaptertest.RunCodingAgent(tb, adaptertest.Spec{
		Descriptor:           coding.Descriptor(),
		Implementation:       coding,
		Contract:             (*managedFlowCodingContract)(nil),
		RequiredCapabilities: []string{"patch.apply", "pull_request"},
	})

	review := &managedFlowReviewAdapter{
		descriptor: &reinv1.Adapter{
			Id:          fakeReviewAdapterID,
			Name:        "Review Bot Fake",
			Category:    reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT,
			Description: "Simulates an approving code review",
			Version:     "0.1.0",
			Enabled:     true,
			Capabilities: map[string]string{
				"approve": "true",
			},
		},
	}
	adaptertest.RunReviewAgent(tb, adaptertest.Spec{
		Descriptor:           review.Descriptor(),
		Implementation:       review,
		Contract:             (*managedFlowReviewContract)(nil),
		RequiredCapabilities: []string{"approve"},
	})

	return &managedFlowAdapterCatalog{
		adapters: map[string]managedFlowAdapter{
			tracker.Descriptor().GetId(): tracker,
			coding.Descriptor().GetId():  coding,
			review.Descriptor().GetId():  review,
		},
	}
}

func (c *managedFlowAdapterCatalog) List() []*reinv1.Adapter {
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

func (c *managedFlowAdapterCatalog) Get(id string) (*reinv1.Adapter, bool) {
	adapter, ok := c.adapters[id]
	if !ok {
		return nil, false
	}
	return adapter.Descriptor(), true
}

func (c *managedFlowAdapterCatalog) Run(ctx context.Context, state *managedFlowState, step *reinv1.WorkflowStep) error {
	adapter, ok := c.adapters[step.GetAdapterId()]
	if !ok {
		return fmt.Errorf("adapter %q not found", step.GetAdapterId())
	}
	return adapter.Run(ctx, state, step)
}

type managedFlowAdapter interface {
	Descriptor() *reinv1.Adapter
	Run(context.Context, *managedFlowState, *reinv1.WorkflowStep) error
}

type managedFlowTrackerContract interface {
	Run(context.Context, *managedFlowState, *reinv1.WorkflowStep) error
}

type managedFlowCodingContract interface {
	Run(context.Context, *managedFlowState, *reinv1.WorkflowStep) error
}

type managedFlowReviewContract interface {
	Run(context.Context, *managedFlowState, *reinv1.WorkflowStep) error
}

type managedFlowTrackerAdapter struct {
	descriptor *reinv1.Adapter
}

func (a *managedFlowTrackerAdapter) Descriptor() *reinv1.Adapter {
	return proto.Clone(a.descriptor).(*reinv1.Adapter)
}

func (a *managedFlowTrackerAdapter) Run(ctx context.Context, state *managedFlowState, step *reinv1.WorkflowStep) error {
	switch step.GetInputs()["operation"] {
	case "prepare":
		slug := slugify(state.issue.GetTitle())
		branch := fmt.Sprintf("issues/%s-%s", strings.ToLower(state.issue.GetId()), slug)
		worktree := fmt.Sprintf("/worktrees/%s-%s", strings.ToLower(state.issue.GetId()), slug)
		state.execution.Metadata["issue_url"] = fmt.Sprintf("https://tracker.fake/issues/%s", state.issue.GetId())
		state.execution.Metadata["branch"] = branch
		state.execution.Metadata["worktree"] = worktree
		state.issue.Labels["branch"] = branch
		state.issue.Labels["worktree"] = worktree
		return state.recordCost(ctx, step, 1200, cost.Usage{InputTokens: 120, OutputTokens: 40}, map[string]string{"operation": "prepare"})
	case "merge":
		if state.execution.Metadata["review_state"] != "APPROVED" {
			return errors.New("review approval missing before merge")
		}
		if state.execution.Metadata["pr_url"] == "" {
			return errors.New("pull request missing before merge")
		}
		baseBranch := state.execution.Metadata["base_branch"]
		if baseBranch == "" {
			baseBranch = "main"
		}
		mergeCommit := "merge-rn-9-001"
		state.execution.Metadata["merge_commit"] = mergeCommit
		state.execution.Metadata["integration_branch"] = baseBranch
		state.issue.Labels["merge_commit"] = mergeCommit
		return state.recordCost(ctx, step, 1100, cost.Usage{InputTokens: 60, OutputTokens: 25}, map[string]string{"operation": "merge"})
	default:
		return fmt.Errorf("unsupported tracker operation %q", step.GetInputs()["operation"])
	}
}

type managedFlowCodingAdapter struct {
	descriptor *reinv1.Adapter
}

func (a *managedFlowCodingAdapter) Descriptor() *reinv1.Adapter {
	return proto.Clone(a.descriptor).(*reinv1.Adapter)
}

func (a *managedFlowCodingAdapter) Run(ctx context.Context, state *managedFlowState, step *reinv1.WorkflowStep) error {
	if len(step.GetDependsOn()) == 0 || state.execution.Metadata["branch"] == "" || state.execution.Metadata["worktree"] == "" {
		return errors.New("branch and worktree must exist before opening a pull request")
	}

	if _, ok := state.execution.Metadata["credential_ref.github_token"]; ok {
		if state.credentials["github_token"] == "" {
			return errors.New("github_token credential must resolve before opening a pull request")
		}
		state.execution.Metadata["auth_mode"] = "credential"
	}

	state.execution.Metadata["pr_url"] = "https://tracker.fake/repos/rein/pull/101"
	state.execution.Metadata["pr_state"] = "OPEN"
	state.issue.Labels["pr_url"] = state.execution.Metadata["pr_url"]
	return state.recordCost(ctx, step, 2400, cost.Usage{InputTokens: 400, OutputTokens: 180}, nil)
}

type managedFlowReviewAdapter struct {
	descriptor *reinv1.Adapter
}

func (a *managedFlowReviewAdapter) Descriptor() *reinv1.Adapter {
	return proto.Clone(a.descriptor).(*reinv1.Adapter)
}

func (a *managedFlowReviewAdapter) Run(ctx context.Context, state *managedFlowState, step *reinv1.WorkflowStep) error {
	if len(step.GetDependsOn()) == 0 || state.execution.Metadata["pr_url"] == "" {
		return errors.New("pull request must exist before review")
	}

	state.execution.Metadata["review_state"] = "APPROVED"
	state.execution.Metadata["reviewed_by"] = a.descriptor.GetId()
	state.issue.Labels["review_state"] = "APPROVED"
	return state.recordCost(ctx, step, 900, cost.Usage{InputTokens: 80, OutputTokens: 30}, nil)
}

func managedFlowWorkflow() *reinv1.Workflow {
	created := timestamppb.New(time.Date(2026, time.April, 30, 12, 0, 0, 0, time.UTC))
	return &reinv1.Workflow{
		Id:          "managed-issue-pr-review-merge",
		Name:        "Managed issue to merge",
		Description: "Fake-backed issue → branch → worktree → PR → review → merge orchestration",
		Version:     "0.1.0",
		CreatedTime: created,
		UpdatedTime: created,
		Steps: []*reinv1.WorkflowStep{
			{
				Id:        "prepare-branch",
				Name:      "Prepare issue branch and worktree",
				AdapterId: fakeTrackerAdapterID,
				Inputs: map[string]string{
					"operation": "prepare",
				},
			},
			{
				Id:        "open-pr",
				Name:      "Open pull request",
				AdapterId: fakeCodingAdapterID,
				DependsOn: []string{"prepare-branch"},
			},
			{
				Id:        "approve-pr",
				Name:      "Approve pull request",
				AdapterId: fakeReviewAdapterID,
				DependsOn: []string{"open-pr"},
			},
			{
				Id:        "merge-pr",
				Name:      "Merge pull request",
				AdapterId: fakeTrackerAdapterID,
				DependsOn: []string{"approve-pr"},
				Inputs: map[string]string{
					"operation": "merge",
				},
			},
		},
	}
}

func validateManagedFlowWorkflow(workflow *reinv1.Workflow, catalog *managedFlowAdapterCatalog) []*reinv1.ValidationMessage {
	var messages []*reinv1.ValidationMessage
	if strings.TrimSpace(workflow.GetId()) == "" {
		messages = append(messages, validationError("workflow.id", "workflow id is required"))
	}
	if strings.TrimSpace(workflow.GetName()) == "" {
		messages = append(messages, validationError("workflow.name", "workflow name is required"))
	}
	if len(workflow.GetSteps()) == 0 {
		messages = append(messages, validationError("workflow.steps", "workflow must declare at least one step"))
	}

	seen := map[string]struct{}{}
	for index, step := range workflow.GetSteps() {
		fieldPrefix := fmt.Sprintf("workflow.steps[%d]", index)
		if strings.TrimSpace(step.GetId()) == "" {
			messages = append(messages, validationError(fieldPrefix+".id", "step id is required"))
			continue
		}
		if _, ok := seen[step.GetId()]; ok {
			messages = append(messages, validationError(fieldPrefix+".id", "step id must be unique"))
			continue
		}
		seen[step.GetId()] = struct{}{}

		if strings.TrimSpace(step.GetAdapterId()) == "" {
			messages = append(messages, validationError(fieldPrefix+".adapter_id", "step adapter_id is required"))
		} else if _, ok := catalog.Get(step.GetAdapterId()); !ok {
			messages = append(messages, validationError(fieldPrefix+".adapter_id", fmt.Sprintf("unknown adapter %q", step.GetAdapterId())))
		}

		for _, dependency := range step.GetDependsOn() {
			if _, ok := seen[dependency]; !ok {
				messages = append(messages, validationError(fieldPrefix+".depends_on", fmt.Sprintf("dependency %q must reference an earlier step", dependency)))
			}
		}
	}

	return messages
}

func validationError(field, message string) *reinv1.ValidationMessage {
	return &reinv1.ValidationMessage{
		Severity: reinv1.ValidationMessage_SEVERITY_ERROR,
		Field:    field,
		Message:  message,
	}
}

func createStoredProto(ctx context.Context, store *sqlite.Store, kind sqlite.EntityKind, id string, message proto.Message) (sqlite.Record, error) {
	payload, err := protojson.Marshal(message)
	if err != nil {
		return sqlite.Record{}, err
	}
	return store.Create(ctx, kind, id, payload)
}

func updateStoredProto(ctx context.Context, store *sqlite.Store, kind sqlite.EntityKind, id string, lockVersion int64, message proto.Message) (sqlite.Record, error) {
	payload, err := protojson.Marshal(message)
	if err != nil {
		return sqlite.Record{}, err
	}
	return store.Update(ctx, kind, id, lockVersion, payload)
}

func loadStoredProject(ctx context.Context, store *sqlite.Store, id string) (*reinv1.Project, sqlite.Record, error) {
	project := &reinv1.Project{}
	record, err := loadStoredProto(ctx, store, sqlite.ProjectKind, id, project)
	return project, record, err
}

func loadStoredIssue(ctx context.Context, store *sqlite.Store, id string) (*reinv1.Issue, sqlite.Record, error) {
	issue := &reinv1.Issue{}
	record, err := loadStoredProto(ctx, store, sqlite.IssueKind, id, issue)
	return issue, record, err
}

func loadStoredWorkflow(ctx context.Context, store *sqlite.Store, id string) (*reinv1.Workflow, sqlite.Record, error) {
	workflow := &reinv1.Workflow{}
	record, err := loadStoredProto(ctx, store, sqlite.WorkflowKind, id, workflow)
	return workflow, record, err
}

func loadStoredExecution(ctx context.Context, store *sqlite.Store, id string) (*reinv1.Execution, sqlite.Record, error) {
	execution := &reinv1.Execution{}
	record, err := loadStoredProto(ctx, store, sqlite.ExecutionKind, id, execution)
	return execution, record, err
}

func loadStoredProto(ctx context.Context, store *sqlite.Store, kind sqlite.EntityKind, id string, message proto.Message) (sqlite.Record, error) {
	record, err := store.Get(ctx, kind, id)
	if err != nil {
		return sqlite.Record{}, err
	}
	if err := protojson.Unmarshal(record.Payload, message); err != nil {
		return sqlite.Record{}, err
	}
	return record, nil
}

func toStatusError(kind, id string, err error) error {
	if errors.Is(err, sqlite.ErrNotFound) {
		return status.Errorf(codes.NotFound, "%s %q not found", kind, id)
	}
	return status.Errorf(codes.Internal, "%s %q: %v", kind, id, err)
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func (s *managedFlowState) recordCost(ctx context.Context, step *reinv1.WorkflowStep, costMicros int64, usage cost.Usage, attributes map[string]string) error {
	if s.costRecorder == nil {
		return nil
	}
	attributes = cloneStringMap(attributes)
	if attributes == nil {
		attributes = map[string]string{}
	}
	attributes["workflow_step_id"] = step.GetId()
	attributes["workflow_step_name"] = step.GetName()
	_, err := s.costRecorder.Record(ctx, cost.Event{
		ID:          fmt.Sprintf("%s-%s", s.execution.GetId(), step.GetId()),
		Name:        "adapter.turn.completed",
		ProjectID:   s.issue.GetProjectId(),
		IssueID:     s.issue.GetId(),
		ExecutionID: s.execution.GetId(),
		WorkflowID:  s.workflow.GetId(),
		AdapterID:   step.GetAdapterId(),
		Currency:    "USD",
		CostMicros:  costMicros,
		Usage:       usage,
		Attributes:  attributes,
	})
	return err
}

func collectCostEvents(tb testing.TB, ctx context.Context, events <-chan cost.Event, want int) []cost.Event {
	tb.Helper()
	collected := make([]cost.Event, 0, want)
	for len(collected) < want {
		select {
		case event := <-events:
			collected = append(collected, event)
		case <-ctx.Done():
			tb.Fatalf("collectCostEvents() timed out after %d/%d events: %v", len(collected), want, ctx.Err())
		}
	}
	return collected
}

func adapterIDs(adapters []*reinv1.Adapter) []string {
	ids := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		ids = append(ids, adapter.GetId())
	}
	slices.Sort(ids)
	return ids
}

func slugify(value string) string {
	var builder strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastHyphen = false
		case !lastHyphen:
			builder.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
