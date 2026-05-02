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
		if _, err := createStoredProto(ctx, store, seed.kind, seed.id, seed.msg); err != nil {
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
