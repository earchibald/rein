package service

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/storage/sqlite"
	"github.com/earchibald/rein/internal/workflow"
)

func TestManagedServersListAndUpdateResources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := sqlite.OpenInMemoryAndMigrate(ctx, t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	created := timestamppb.New(time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC))
	projectOne := &reinv1.Project{
		Id:          "project-alpha",
		Slug:        "alpha",
		DisplayName: "Alpha",
		Summary:     "First project",
		Status:      reinv1.ProjectStatus_PROJECT_STATUS_ACTIVE,
		CreatedTime: created,
		UpdatedTime: created,
	}
	projectTwo := &reinv1.Project{
		Id:          "project-beta",
		Slug:        "beta",
		DisplayName: "Beta",
		Summary:     "Archived project",
		Status:      reinv1.ProjectStatus_PROJECT_STATUS_ARCHIVED,
		CreatedTime: created,
		UpdatedTime: created,
	}
	issueOne := &reinv1.Issue{
		Id:          "RN-17",
		ProjectId:   projectOne.GetId(),
		Title:       "Canonical CLI",
		Summary:     "Implement canonical command surface",
		Status:      reinv1.IssueStatus_ISSUE_STATUS_OPEN,
		Priority:    reinv1.IssuePriority_ISSUE_PRIORITY_HIGH,
		WorkflowId:  "workflow-release",
		Assignee:    "copilot",
		CreatedTime: created,
		UpdatedTime: created,
	}
	issueTwo := &reinv1.Issue{
		Id:          "RN-18",
		ProjectId:   projectTwo.GetId(),
		Title:       "Other work",
		Summary:     "Different issue",
		Status:      reinv1.IssueStatus_ISSUE_STATUS_RESOLVED,
		Priority:    reinv1.IssuePriority_ISSUE_PRIORITY_LOW,
		WorkflowId:  "workflow-maintenance",
		Assignee:    "teammate",
		CreatedTime: created,
		UpdatedTime: created,
	}
	executionOne := &reinv1.Execution{
		Id:          "exec-RN-17-001",
		IssueId:     issueOne.GetId(),
		WorkflowId:  issueOne.GetWorkflowId(),
		Status:      reinv1.ExecutionStatus_EXECUTION_STATUS_RUNNING,
		RequestedBy: "copilot",
		CreatedTime: created,
		StartedTime: created,
	}
	executionTwo := &reinv1.Execution{
		Id:           "exec-RN-18-001",
		IssueId:      issueTwo.GetId(),
		WorkflowId:   issueTwo.GetWorkflowId(),
		Status:       reinv1.ExecutionStatus_EXECUTION_STATUS_SUCCEEDED,
		RequestedBy:  "teammate",
		CreatedTime:  created,
		FinishedTime: created,
	}
	workflowOne := &reinv1.Workflow{
		Id:          "workflow-release",
		Name:        "Release Flow",
		Description: "Ship the change",
		Version:     "v1",
		CreatedTime: created,
		UpdatedTime: created,
	}
	workflowTwo := &reinv1.Workflow{
		Id:          "workflow-maintenance",
		Name:        "Maintenance Flow",
		Description: "Keep the system healthy",
		Version:     "v1",
		CreatedTime: created,
		UpdatedTime: created,
	}

	for _, seed := range []struct {
		kind sqlite.EntityKind
		id   string
		msg  proto.Message
	}{
		{kind: sqlite.ProjectKind, id: projectOne.GetId(), msg: projectOne},
		{kind: sqlite.ProjectKind, id: projectTwo.GetId(), msg: projectTwo},
		{kind: sqlite.IssueKind, id: issueOne.GetId(), msg: issueOne},
		{kind: sqlite.IssueKind, id: issueTwo.GetId(), msg: issueTwo},
		{kind: sqlite.ExecutionKind, id: executionOne.GetId(), msg: executionOne},
		{kind: sqlite.ExecutionKind, id: executionTwo.GetId(), msg: executionTwo},
		{kind: sqlite.WorkflowKind, id: workflowOne.GetId(), msg: workflowOne},
		{kind: sqlite.WorkflowKind, id: workflowTwo.GetId(), msg: workflowTwo},
	} {
		if err := createStoredProto(ctx, store, seed.kind, seed.id, seed.msg); err != nil {
			t.Fatalf("createStoredProto(%s) error = %v", seed.id, err)
		}
	}

	projectServer := &ManagedProjectServer{store: store}
	projectList, err := projectServer.ListProjects(ctx, &reinv1.ListProjectsRequest{
		Status: reinv1.ProjectStatus_PROJECT_STATUS_ACTIVE,
		Query:  "alpha",
	})
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projectList.GetProjects()) != 1 || projectList.GetProjects()[0].GetId() != projectOne.GetId() {
		t.Fatalf("ListProjects() = %+v", projectList.GetProjects())
	}

	updatedProject, err := projectServer.UpdateProject(ctx, &reinv1.UpdateProjectRequest{Project: &reinv1.Project{
		Id:          projectOne.GetId(),
		Slug:        "alpha",
		DisplayName: "Alpha Updated",
		Summary:     "Updated summary",
	}})
	if err != nil {
		t.Fatalf("UpdateProject() error = %v", err)
	}
	if updatedProject.GetProject().GetStatus() != reinv1.ProjectStatus_PROJECT_STATUS_ACTIVE {
		t.Fatalf("UpdateProject() status = %s, want active", updatedProject.GetProject().GetStatus())
	}
	if updatedProject.GetProject().GetCreatedTime().AsTime() != created.AsTime() {
		t.Fatalf("UpdateProject() created_time changed = %v", updatedProject.GetProject().GetCreatedTime())
	}

	issueServer := &ManagedIssueServer{store: store}
	issueList, err := issueServer.ListIssues(ctx, &reinv1.ListIssuesRequest{
		ProjectId: projectOne.GetId(),
		Assignee:  "copilot",
		Query:     "canonical",
	})
	if err != nil {
		t.Fatalf("ListIssues() error = %v", err)
	}
	if len(issueList.GetIssues()) != 1 || issueList.GetIssues()[0].GetId() != issueOne.GetId() {
		t.Fatalf("ListIssues() = %+v", issueList.GetIssues())
	}

	updatedIssue, err := issueServer.UpdateIssue(ctx, &reinv1.UpdateIssueRequest{Issue: &reinv1.Issue{
		Id:         issueOne.GetId(),
		ProjectId:  issueOne.GetProjectId(),
		Title:      "Canonical CLI updated",
		Summary:    "Updated issue summary",
		Status:     reinv1.IssueStatus_ISSUE_STATUS_IN_PROGRESS,
		Priority:   reinv1.IssuePriority_ISSUE_PRIORITY_HIGH,
		WorkflowId: issueOne.GetWorkflowId(),
		Assignee:   "copilot",
	}})
	if err != nil {
		t.Fatalf("UpdateIssue() error = %v", err)
	}
	if updatedIssue.GetIssue().GetStatus() != reinv1.IssueStatus_ISSUE_STATUS_IN_PROGRESS {
		t.Fatalf("UpdateIssue() status = %s, want in_progress", updatedIssue.GetIssue().GetStatus())
	}
	if updatedIssue.GetIssue().GetCreatedTime().AsTime() != created.AsTime() {
		t.Fatalf("UpdateIssue() created_time changed = %v", updatedIssue.GetIssue().GetCreatedTime())
	}

	executionServer := &ManagedExecutionServer{store: store}
	executionList, err := executionServer.ListExecutions(ctx, &reinv1.ListExecutionsRequest{
		IssueId: issueOne.GetId(),
		Status:  reinv1.ExecutionStatus_EXECUTION_STATUS_RUNNING,
	})
	if err != nil {
		t.Fatalf("ListExecutions() error = %v", err)
	}
	if len(executionList.GetExecutions()) != 1 || executionList.GetExecutions()[0].GetId() != executionOne.GetId() {
		t.Fatalf("ListExecutions() = %+v", executionList.GetExecutions())
	}

	workflowServer := &ManagedWorkflowServer{store: store}
	workflowList, err := workflowServer.ListWorkflows(ctx, &reinv1.ListWorkflowsRequest{
		Query: "release",
		Page:  &reinv1.PageRequest{PageSize: 1},
	})
	if err != nil {
		t.Fatalf("ListWorkflows() error = %v", err)
	}
	if len(workflowList.GetWorkflows()) != 1 || workflowList.GetWorkflows()[0].GetId() != workflowOne.GetId() {
		t.Fatalf("ListWorkflows() = %+v", workflowList.GetWorkflows())
	}
}

func TestManagedAdapterServerListFilters(t *testing.T) {
	t.Parallel()

	server := &ManagedAdapterServer{catalog: managedTestCatalog{
		adapters: map[string]ManagedAdapter{
			"coding": managedTestAdapter{descriptor: &reinv1.Adapter{Id: "coding", Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT, Enabled: true}},
			"review": managedTestAdapter{descriptor: &reinv1.Adapter{Id: "review", Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT, Enabled: false}},
		},
	}}

	resp, err := server.ListAdapters(context.Background(), &reinv1.ListAdaptersRequest{
		Category:    reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
		EnabledOnly: true,
	})
	if err != nil {
		t.Fatalf("ListAdapters() error = %v", err)
	}
	if len(resp.GetAdapters()) != 1 || resp.GetAdapters()[0].GetId() != "coding" {
		t.Fatalf("ListAdapters() = %+v", resp.GetAdapters())
	}
}

type managedTestCatalog struct {
	adapters map[string]ManagedAdapter
}

func (c managedTestCatalog) List() []*reinv1.Adapter {
	list := make([]*reinv1.Adapter, 0, len(c.adapters))
	for _, adapter := range c.adapters {
		list = append(list, adapter.Descriptor())
	}
	return list
}

func (c managedTestCatalog) Lookup(id string) (ManagedAdapter, bool) {
	adapter, ok := c.adapters[id]
	return adapter, ok
}

type managedTestAdapter struct {
	descriptor *reinv1.Adapter
}

func (a managedTestAdapter) Descriptor() *reinv1.Adapter {
	return a.descriptor
}

func (managedTestAdapter) Run(context.Context, *workflow.RuntimeState, workflow.Phase, workflow.Direction, *workflow.SideEffect) error {
	return nil
}

type managedConcurrentUpdateAdapter struct {
	descriptor *reinv1.Adapter
	store      *sqlite.Store
}

func (a managedConcurrentUpdateAdapter) Descriptor() *reinv1.Adapter {
	return a.descriptor
}

func (a managedConcurrentUpdateAdapter) Run(ctx context.Context, state *workflow.RuntimeState, _ workflow.Phase, _ workflow.Direction, _ *workflow.SideEffect) error {
	issueServer := &ManagedIssueServer{store: a.store}
	_, err := issueServer.UpdateIssue(ctx, &reinv1.UpdateIssueRequest{Issue: &reinv1.Issue{
		Id:         state.Issue.GetId(),
		ProjectId:  state.Issue.GetProjectId(),
		Title:      state.Issue.GetTitle(),
		Summary:    state.Issue.GetSummary(),
		Status:     state.Issue.GetStatus(),
		Priority:   state.Issue.GetPriority(),
		WorkflowId: state.Issue.GetWorkflowId(),
		Assignee:   state.Issue.GetAssignee(),
		Labels:     map[string]string{"team": "core"},
	}})
	return err
}

func TestManagedExecutionServerStartExecutionCreateFailureDoesNotMutateIssueState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := sqlite.OpenInMemoryAndMigrate(ctx, t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	created := timestamppb.New(time.Date(2026, time.May, 2, 12, 0, 0, 0, time.UTC))
	issue := &reinv1.Issue{
		Id:          "RN-35",
		ProjectId:   "project-rein",
		Title:       "Execution integrity",
		Status:      reinv1.IssueStatus_ISSUE_STATUS_OPEN,
		WorkflowId:  "workflow-noop",
		CreatedTime: created,
		UpdatedTime: created,
	}
	workflowEntity := &reinv1.Workflow{
		Id:          "workflow-noop",
		Name:        "No-op workflow",
		Version:     "v1",
		CreatedTime: created,
		UpdatedTime: created,
		Steps: []*reinv1.WorkflowStep{{
			Id:        "noop",
			Name:      "No-op",
			AdapterId: "noop",
		}},
	}
	existingExecution := &reinv1.Execution{
		Id:           "exec-rn-35-collision",
		IssueId:      issue.GetId(),
		WorkflowId:   workflowEntity.GetId(),
		Status:       reinv1.ExecutionStatus_EXECUTION_STATUS_SUCCEEDED,
		RequestedBy:  "copilot",
		CreatedTime:  created,
		StartedTime:  created,
		FinishedTime: created,
	}

	for _, seed := range []struct {
		kind sqlite.EntityKind
		id   string
		msg  proto.Message
	}{
		{kind: sqlite.IssueKind, id: issue.GetId(), msg: issue},
		{kind: sqlite.WorkflowKind, id: workflowEntity.GetId(), msg: workflowEntity},
		{kind: sqlite.ExecutionKind, id: existingExecution.GetId(), msg: existingExecution},
	} {
		if err := createStoredProto(ctx, store, seed.kind, seed.id, seed.msg); err != nil {
			t.Fatalf("createStoredProto(%s) error = %v", seed.id, err)
		}
	}

	server := &ManagedExecutionServer{
		catalog: managedTestCatalog{adapters: map[string]ManagedAdapter{
			"noop": managedTestAdapter{descriptor: &reinv1.Adapter{Id: "noop", Enabled: true}},
		}},
		engine:         workflow.New(store),
		store:          store,
		newExecutionID: func(string) string { return existingExecution.GetId() },
	}

	if _, err := server.StartExecution(ctx, &reinv1.StartExecutionRequest{
		IssueId:     issue.GetId(),
		WorkflowId:  workflowEntity.GetId(),
		RequestedBy: "copilot",
	}); err == nil {
		t.Fatal("StartExecution() error = nil, want collision failure")
	}

	storedIssue := &reinv1.Issue{}
	if _, err := loadStoredProto(ctx, store, sqlite.IssueKind, issue.GetId(), storedIssue); err != nil {
		t.Fatalf("loadStoredProto(issue) error = %v", err)
	}
	normalizeIssue(storedIssue)
	if got := storedIssue.GetStatus(); got != reinv1.IssueStatus_ISSUE_STATUS_OPEN {
		t.Fatalf("stored issue status = %s, want open", got)
	}
	if storedIssue.GetDaemonState() != nil {
		t.Fatalf("stored issue daemon_state = %+v, want nil", storedIssue.GetDaemonState())
	}
}

func TestManagedExecutionServerStartExecutionGeneratesUniqueIDsAcrossRetries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := sqlite.OpenInMemoryAndMigrate(ctx, t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	created := timestamppb.New(time.Date(2026, time.May, 2, 13, 0, 0, 0, time.UTC))
	issue := &reinv1.Issue{
		Id:          "RN-36",
		ProjectId:   "project-rein",
		Title:       "Retry execution",
		Status:      reinv1.IssueStatus_ISSUE_STATUS_OPEN,
		WorkflowId:  "workflow-noop",
		CreatedTime: created,
		UpdatedTime: created,
	}
	workflowEntity := &reinv1.Workflow{
		Id:          "workflow-noop",
		Name:        "No-op workflow",
		Version:     "v1",
		CreatedTime: created,
		UpdatedTime: created,
		Steps: []*reinv1.WorkflowStep{{
			Id:        "noop",
			Name:      "No-op",
			AdapterId: "noop",
		}},
	}
	if err := createStoredProto(ctx, store, sqlite.IssueKind, issue.GetId(), issue); err != nil {
		t.Fatalf("createStoredProto(issue) error = %v", err)
	}
	if err := createStoredProto(ctx, store, sqlite.WorkflowKind, workflowEntity.GetId(), workflowEntity); err != nil {
		t.Fatalf("createStoredProto(workflow) error = %v", err)
	}

	server := &ManagedExecutionServer{
		catalog: managedTestCatalog{adapters: map[string]ManagedAdapter{
			"noop": managedTestAdapter{descriptor: &reinv1.Adapter{Id: "noop", Enabled: true}},
		}},
		engine: workflow.New(store),
		store:  store,
	}

	first, err := server.StartExecution(ctx, &reinv1.StartExecutionRequest{
		IssueId:     issue.GetId(),
		WorkflowId:  workflowEntity.GetId(),
		RequestedBy: "copilot",
	})
	if err != nil {
		t.Fatalf("StartExecution(first) error = %v", err)
	}
	second, err := server.StartExecution(ctx, &reinv1.StartExecutionRequest{
		IssueId:     issue.GetId(),
		WorkflowId:  workflowEntity.GetId(),
		RequestedBy: "copilot",
	})
	if err != nil {
		t.Fatalf("StartExecution(second) error = %v", err)
	}
	if first.GetExecution().GetId() == second.GetExecution().GetId() {
		t.Fatalf("execution ids = %q and %q, want unique values", first.GetExecution().GetId(), second.GetExecution().GetId())
	}

	storedIssue := &reinv1.Issue{}
	if _, err := loadStoredProto(ctx, store, sqlite.IssueKind, issue.GetId(), storedIssue); err != nil {
		t.Fatalf("loadStoredProto(issue) error = %v", err)
	}
	normalizeIssue(storedIssue)
	if got := storedIssue.GetDaemonState().GetExecutionId(); got != second.GetExecution().GetId() {
		t.Fatalf("daemon_state.execution_id = %q, want %q", got, second.GetExecution().GetId())
	}
	if got := storedIssue.GetDaemonState().GetIntegrationStatus(); got != "merged" {
		t.Fatalf("daemon_state.integration_status = %q, want merged", got)
	}
}

func TestManagedIssueServerUpdatePreservesDaemonState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := sqlite.OpenInMemoryAndMigrate(ctx, t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	created := timestamppb.New(time.Date(2026, time.May, 2, 14, 0, 0, 0, time.UTC))
	issue := &reinv1.Issue{
		Id:          "RN-37",
		ProjectId:   "project-rein",
		Title:       "Preserve daemon state",
		Status:      reinv1.IssueStatus_ISSUE_STATUS_OPEN,
		Priority:    reinv1.IssuePriority_ISSUE_PRIORITY_HIGH,
		WorkflowId:  "workflow-noop",
		CreatedTime: created,
		UpdatedTime: created,
		DaemonState: &reinv1.IssueDaemonState{
			ExecutionId:       "exec-rn-37-previous",
			Branch:            "issues/rn-37-preserve-daemon-state",
			IntegrationStatus: "running",
		},
	}
	if err := createStoredProto(ctx, store, sqlite.IssueKind, issue.GetId(), issue); err != nil {
		t.Fatalf("createStoredProto(issue) error = %v", err)
	}

	server := &ManagedIssueServer{store: store}
	resp, err := server.UpdateIssue(ctx, &reinv1.UpdateIssueRequest{Issue: &reinv1.Issue{
		Id:         issue.GetId(),
		ProjectId:  issue.GetProjectId(),
		Title:      "Preserve daemon state updated",
		Summary:    "Labels should not clobber daemon state",
		Status:     reinv1.IssueStatus_ISSUE_STATUS_IN_PROGRESS,
		Priority:   issue.GetPriority(),
		WorkflowId: issue.GetWorkflowId(),
		Assignee:   "copilot",
		Labels:     map[string]string{"team": "core"},
	}})
	if err != nil {
		t.Fatalf("UpdateIssue() error = %v", err)
	}

	updated := resp.GetIssue()
	if got := updated.GetDaemonState().GetExecutionId(); got != issue.GetDaemonState().GetExecutionId() {
		t.Fatalf("daemon_state.execution_id = %q, want %q", got, issue.GetDaemonState().GetExecutionId())
	}
	if got := updated.GetDaemonState().GetBranch(); got != issue.GetDaemonState().GetBranch() {
		t.Fatalf("daemon_state.branch = %q, want %q", got, issue.GetDaemonState().GetBranch())
	}
	if got := updated.GetLabels()["team"]; got != "core" {
		t.Fatalf("labels[team] = %q, want core", got)
	}
	if got := updated.GetLabels()["branch"]; got != "" {
		t.Fatalf("labels[branch] = %q, want empty", got)
	}
}

func TestManagedExecutionServerMergesConcurrentIssueUpdatesBeforeCompletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := sqlite.OpenInMemoryAndMigrate(ctx, t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	created := timestamppb.New(time.Date(2026, time.May, 2, 15, 0, 0, 0, time.UTC))
	issue := &reinv1.Issue{
		Id:          "RN-38",
		ProjectId:   "project-rein",
		Title:       "Merge concurrent updates",
		Status:      reinv1.IssueStatus_ISSUE_STATUS_OPEN,
		Priority:    reinv1.IssuePriority_ISSUE_PRIORITY_HIGH,
		WorkflowId:  "workflow-concurrent",
		CreatedTime: created,
		UpdatedTime: created,
	}
	workflowEntity := &reinv1.Workflow{
		Id:          "workflow-concurrent",
		Name:        "Concurrent merge workflow",
		Version:     "v1",
		CreatedTime: created,
		UpdatedTime: created,
		Steps: []*reinv1.WorkflowStep{{
			Id:        "concurrent",
			Name:      "Concurrent issue update",
			AdapterId: "concurrent",
		}},
	}
	if err := createStoredProto(ctx, store, sqlite.IssueKind, issue.GetId(), issue); err != nil {
		t.Fatalf("createStoredProto(issue) error = %v", err)
	}
	if err := createStoredProto(ctx, store, sqlite.WorkflowKind, workflowEntity.GetId(), workflowEntity); err != nil {
		t.Fatalf("createStoredProto(workflow) error = %v", err)
	}

	server := &ManagedExecutionServer{
		catalog: managedTestCatalog{adapters: map[string]ManagedAdapter{
			"concurrent": managedConcurrentUpdateAdapter{
				descriptor: &reinv1.Adapter{Id: "concurrent", Enabled: true},
				store:      store,
			},
		}},
		engine: workflow.New(store),
		store:  store,
	}

	resp, err := server.StartExecution(ctx, &reinv1.StartExecutionRequest{
		IssueId:     issue.GetId(),
		WorkflowId:  workflowEntity.GetId(),
		RequestedBy: "copilot",
	})
	if err != nil {
		t.Fatalf("StartExecution() error = %v", err)
	}
	if got := resp.GetExecution().GetStatus(); got != reinv1.ExecutionStatus_EXECUTION_STATUS_SUCCEEDED {
		t.Fatalf("execution status = %s, want succeeded", got)
	}

	storedIssue := &reinv1.Issue{}
	if _, err := loadStoredProto(ctx, store, sqlite.IssueKind, issue.GetId(), storedIssue); err != nil {
		t.Fatalf("loadStoredProto(issue) error = %v", err)
	}
	normalizeIssue(storedIssue)
	if got := storedIssue.GetLabels()["team"]; got != "core" {
		t.Fatalf("labels[team] = %q, want core", got)
	}
	if got := storedIssue.GetDaemonState().GetIntegrationStatus(); got != "merged" {
		t.Fatalf("daemon_state.integration_status = %q, want merged", got)
	}
}

func TestDeriveIssuePrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id   string
		want string
	}{
		{"rein-demo", "RD"},
		{"my-project", "MP"},
		{"single", "SI"},
		{"a", "A"},
		{"rein-native-agent", "RNA"},
		{"foo_bar_baz", "FBB"},
		{"rein", "RE"},
		{"x-y-z", "XYZ"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got := deriveIssuePrefix(tc.id)
			if got != tc.want {
				t.Errorf("deriveIssuePrefix(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestCreateProjectAutoPrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := sqlite.OpenInMemoryAndMigrate(ctx, t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := &ManagedProjectServer{store: store}

	// prefix derived automatically
	resp, err := svc.CreateProject(ctx, &reinv1.CreateProjectRequest{
		Project: &reinv1.Project{Id: "rein-demo", Slug: "rein-demo"},
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if got := resp.GetProject().GetIssuePrefix(); got != "RD" {
		t.Errorf("issue_prefix = %q, want RD", got)
	}

	// duplicate prefix rejected
	_, err = svc.CreateProject(ctx, &reinv1.CreateProjectRequest{
		Project: &reinv1.Project{Id: "react-design", Slug: "react-design"},
	})
	if err == nil {
		t.Fatal("expected error for duplicate issue_prefix RD, got nil")
	}

	// explicit prefix accepted
	resp2, err := svc.CreateProject(ctx, &reinv1.CreateProjectRequest{
		Project: &reinv1.Project{Id: "react-design", Slug: "react-design", IssuePrefix: "RDS"},
	})
	if err != nil {
		t.Fatalf("CreateProject(explicit prefix) error = %v", err)
	}
	if got := resp2.GetProject().GetIssuePrefix(); got != "RDS" {
		t.Errorf("issue_prefix = %q, want RDS", got)
	}

	// duplicate slug rejected
	_, err = svc.CreateProject(ctx, &reinv1.CreateProjectRequest{
		Project: &reinv1.Project{Id: "another-project", Slug: "rein-demo", IssuePrefix: "AP"},
	})
	if err == nil {
		t.Fatal("expected error for duplicate slug, got nil")
	}
}

func TestCreateIssueAutoID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := sqlite.OpenInMemoryAndMigrate(ctx, t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	projectSvc := &ManagedProjectServer{store: store}
	issueSvc := &ManagedIssueServer{store: store}

	if _, err := projectSvc.CreateProject(ctx, &reinv1.CreateProjectRequest{
		Project: &reinv1.Project{Id: "rein-demo"},
	}); err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}

	// First auto-generated issue should be RD-1.
	r1, err := issueSvc.CreateIssue(ctx, &reinv1.CreateIssueRequest{
		Issue: &reinv1.Issue{ProjectId: "rein-demo", Title: "First issue"},
	})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if got := r1.GetIssue().GetId(); got != "RD-1" {
		t.Errorf("auto id = %q, want RD-1", got)
	}

	// Second auto-generated issue should be RD-2.
	r2, err := issueSvc.CreateIssue(ctx, &reinv1.CreateIssueRequest{
		Issue: &reinv1.Issue{ProjectId: "rein-demo", Title: "Second issue"},
	})
	if err != nil {
		t.Fatalf("CreateIssue() second error = %v", err)
	}
	if got := r2.GetIssue().GetId(); got != "RD-2" {
		t.Errorf("auto id = %q, want RD-2", got)
	}

	// Caller-supplied id is respected.
	r3, err := issueSvc.CreateIssue(ctx, &reinv1.CreateIssueRequest{
		Issue: &reinv1.Issue{Id: "RD-99", ProjectId: "rein-demo", Title: "Pinned issue"},
	})
	if err != nil {
		t.Fatalf("CreateIssue(explicit id) error = %v", err)
	}
	if got := r3.GetIssue().GetId(); got != "RD-99" {
		t.Errorf("explicit id = %q, want RD-99", got)
	}

	// Next auto after RD-99 should be RD-100.
	r4, err := issueSvc.CreateIssue(ctx, &reinv1.CreateIssueRequest{
		Issue: &reinv1.Issue{ProjectId: "rein-demo", Title: "After gap"},
	})
	if err != nil {
		t.Fatalf("CreateIssue() after gap error = %v", err)
	}
	if got := r4.GetIssue().GetId(); got != "RD-100" {
		t.Errorf("auto id after gap = %q, want RD-100", got)
	}
}

func TestUpdateProjectPreservesIssuePrefixAndRepoPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := sqlite.OpenInMemoryAndMigrate(ctx, t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := &ManagedProjectServer{store: store}

	// Create with auto-derived prefix and a repo_path.
	createResp, err := svc.CreateProject(ctx, &reinv1.CreateProjectRequest{
		Project: &reinv1.Project{
			Id:       "rein-demo",
			RepoPath: "/tmp/rein-demo",
		},
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if got := createResp.GetProject().GetIssuePrefix(); got != "RD" {
		t.Fatalf("create: issue_prefix = %q, want RD", got)
	}

	// Update omitting both issue_prefix and repo_path — both should be preserved.
	updateResp, err := svc.UpdateProject(ctx, &reinv1.UpdateProjectRequest{
		Project: &reinv1.Project{
			Id:          "rein-demo",
			DisplayName: "Rein Demo Updated",
		},
	})
	if err != nil {
		t.Fatalf("UpdateProject() error = %v", err)
	}
	if got := updateResp.GetProject().GetIssuePrefix(); got != "RD" {
		t.Errorf("update: issue_prefix = %q, want RD", got)
	}
	if got := updateResp.GetProject().GetRepoPath(); got != "/tmp/rein-demo" {
		t.Errorf("update: repo_path = %q, want /tmp/rein-demo", got)
	}

	// Attempting to change issue_prefix should be rejected.
	_, err = svc.UpdateProject(ctx, &reinv1.UpdateProjectRequest{
		Project: &reinv1.Project{Id: "rein-demo", IssuePrefix: "XX"},
	})
	if err == nil {
		t.Fatal("expected error when changing issue_prefix, got nil")
	}

	// repo_path can be changed via update.
	updateResp2, err := svc.UpdateProject(ctx, &reinv1.UpdateProjectRequest{
		Project: &reinv1.Project{Id: "rein-demo", RepoPath: "/projects/rein-demo"},
	})
	if err != nil {
		t.Fatalf("UpdateProject(new repo_path) error = %v", err)
	}
	if got := updateResp2.GetProject().GetRepoPath(); got != "/projects/rein-demo" {
		t.Errorf("update: repo_path = %q, want /projects/rein-demo", got)
	}
}
