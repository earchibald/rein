package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/storage/sqlite"
	"github.com/earchibald/rein/internal/workflow"
)

type ManagedAdapter interface {
	Descriptor() *reinv1.Adapter
	Run(context.Context, *workflow.RuntimeState, workflow.Phase, workflow.Direction, *workflow.SideEffect) error
}

type ManagedCatalog interface {
	List() []*reinv1.Adapter
	Lookup(string) (ManagedAdapter, bool)
}

func NewManagedSet(store *sqlite.Store, catalog ManagedCatalog) Set {
	engine := workflow.New(store)
	return Set{
		Adapter:   &ManagedAdapterServer{catalog: catalog},
		Execution: &ManagedExecutionServer{catalog: catalog, engine: engine, store: store},
		Issue:     &ManagedIssueServer{store: store},
		Project:   &ManagedProjectServer{store: store},
		Workflow:  &ManagedWorkflowServer{catalog: catalog, engine: engine, store: store},
	}
}

type ManagedAdapterServer struct {
	reinv1.UnimplementedAdapterServiceServer
	catalog ManagedCatalog
}

func (s *ManagedAdapterServer) ListAdapters(_ context.Context, req *reinv1.ListAdaptersRequest) (*reinv1.ListAdaptersResponse, error) {
	if s.catalog == nil {
		return nil, status.Error(codes.FailedPrecondition, "adapter catalog is not configured")
	}

	adapters := s.catalog.List()
	filtered := make([]*reinv1.Adapter, 0, len(adapters))
	for _, adapter := range adapters {
		if adapter == nil {
			continue
		}
		if req.GetCategory() != reinv1.AdapterCategory_ADAPTER_CATEGORY_UNSPECIFIED && adapter.GetCategory() != req.GetCategory() {
			continue
		}
		if req.GetEnabledOnly() && !adapter.GetEnabled() {
			continue
		}
		filtered = append(filtered, adapter)
	}

	paged, page, err := paginateResults(filtered, req.GetPage())
	if err != nil {
		return nil, err
	}
	return &reinv1.ListAdaptersResponse{Adapters: paged, Page: page}, nil
}

func (s *ManagedAdapterServer) GetAdapter(_ context.Context, req *reinv1.GetAdapterRequest) (*reinv1.GetAdapterResponse, error) {
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	adapter, ok := s.catalog.Lookup(req.GetId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "adapter %q not found", req.GetId())
	}
	return &reinv1.GetAdapterResponse{Adapter: adapter.Descriptor()}, nil
}

func (s *ManagedAdapterServer) ValidateAdapter(_ context.Context, req *reinv1.ValidateAdapterRequest) (*reinv1.ValidateAdapterResponse, error) {
	if req.GetAdapter() == nil {
		return nil, status.Error(codes.InvalidArgument, "adapter is required")
	}
	var messages []*reinv1.ValidationMessage
	if strings.TrimSpace(req.GetAdapter().GetId()) == "" {
		messages = append(messages, &reinv1.ValidationMessage{Severity: reinv1.ValidationMessage_SEVERITY_ERROR, Field: "adapter.id", Message: "adapter id is required"})
	}
	if strings.TrimSpace(req.GetAdapter().GetName()) == "" {
		messages = append(messages, &reinv1.ValidationMessage{Severity: reinv1.ValidationMessage_SEVERITY_ERROR, Field: "adapter.name", Message: "adapter name is required"})
	}
	if req.GetAdapter().GetCategory() == reinv1.AdapterCategory_ADAPTER_CATEGORY_UNSPECIFIED {
		messages = append(messages, &reinv1.ValidationMessage{Severity: reinv1.ValidationMessage_SEVERITY_ERROR, Field: "adapter.category", Message: "adapter category is required"})
	}
	return &reinv1.ValidateAdapterResponse{Valid: len(messages) == 0, Messages: messages}, nil
}

type ManagedWorkflowServer struct {
	reinv1.UnimplementedWorkflowServiceServer
	catalog ManagedCatalog
	engine  *workflow.Engine
	store   *sqlite.Store
}

func (s *ManagedWorkflowServer) GetWorkflow(ctx context.Context, req *reinv1.GetWorkflowRequest) (*reinv1.GetWorkflowResponse, error) {
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	workflowEntity := &reinv1.Workflow{}
	if _, err := loadStoredProto(ctx, s.store, sqlite.WorkflowKind, req.GetId(), workflowEntity); err != nil {
		return nil, toStatusError("workflow", req.GetId(), err)
	}
	return &reinv1.GetWorkflowResponse{Workflow: workflowEntity}, nil
}

func (s *ManagedWorkflowServer) ListWorkflows(ctx context.Context, req *reinv1.ListWorkflowsRequest) (*reinv1.ListWorkflowsResponse, error) {
	workflows, err := listStoredMessages(ctx, s.store, sqlite.WorkflowKind, func() *reinv1.Workflow { return &reinv1.Workflow{} })
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list workflows: %v", err)
	}

	query := strings.ToLower(strings.TrimSpace(req.GetQuery()))
	filtered := make([]*reinv1.Workflow, 0, len(workflows))
	for _, workflowEntity := range workflows {
		if query != "" && !matchesQuery(query, workflowEntity.GetId(), workflowEntity.GetName(), workflowEntity.GetDescription(), workflowEntity.GetVersion()) {
			continue
		}
		filtered = append(filtered, workflowEntity)
	}

	paged, page, err := paginateResults(filtered, req.GetPage())
	if err != nil {
		return nil, err
	}
	return &reinv1.ListWorkflowsResponse{Workflows: paged, Page: page}, nil
}

func (s *ManagedWorkflowServer) ValidateWorkflow(_ context.Context, req *reinv1.ValidateWorkflowRequest) (*reinv1.ValidateWorkflowResponse, error) {
	if req.GetWorkflow() == nil {
		return nil, status.Error(codes.InvalidArgument, "workflow is required")
	}
	messages := s.engine.Validate(req.GetWorkflow(), func(id string) bool {
		_, ok := s.catalog.Lookup(id)
		return ok
	})
	return &reinv1.ValidateWorkflowResponse{Valid: len(messages) == 0, Messages: messages}, nil
}

type ManagedProjectServer struct {
	reinv1.UnimplementedProjectServiceServer
	store *sqlite.Store
}

func (s *ManagedProjectServer) ListProjects(ctx context.Context, req *reinv1.ListProjectsRequest) (*reinv1.ListProjectsResponse, error) {
	projects, err := listStoredMessages(ctx, s.store, sqlite.ProjectKind, func() *reinv1.Project { return &reinv1.Project{} })
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list projects: %v", err)
	}

	query := strings.ToLower(strings.TrimSpace(req.GetQuery()))
	filtered := make([]*reinv1.Project, 0, len(projects))
	for _, project := range projects {
		if req.GetStatus() != reinv1.ProjectStatus_PROJECT_STATUS_UNSPECIFIED && project.GetStatus() != req.GetStatus() {
			continue
		}
		if query != "" && !matchesQuery(query, project.GetId(), project.GetSlug(), project.GetDisplayName(), project.GetSummary()) {
			continue
		}
		filtered = append(filtered, project)
	}

	paged, page, err := paginateResults(filtered, req.GetPage())
	if err != nil {
		return nil, err
	}
	return &reinv1.ListProjectsResponse{Projects: paged, Page: page}, nil
}

func (s *ManagedProjectServer) CreateProject(ctx context.Context, req *reinv1.CreateProjectRequest) (*reinv1.CreateProjectResponse, error) {
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

func (s *ManagedProjectServer) GetProject(ctx context.Context, req *reinv1.GetProjectRequest) (*reinv1.GetProjectResponse, error) {
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	project := &reinv1.Project{}
	if _, err := loadStoredProto(ctx, s.store, sqlite.ProjectKind, req.GetId(), project); err != nil {
		return nil, toStatusError("project", req.GetId(), err)
	}
	return &reinv1.GetProjectResponse{Project: project}, nil
}

func (s *ManagedProjectServer) UpdateProject(ctx context.Context, req *reinv1.UpdateProjectRequest) (*reinv1.UpdateProjectResponse, error) {
	if req.GetProject() == nil {
		return nil, status.Error(codes.InvalidArgument, "project is required")
	}

	project := proto.Clone(req.GetProject()).(*reinv1.Project)
	if strings.TrimSpace(project.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "project.id is required")
	}

	current := &reinv1.Project{}
	record, err := loadStoredProto(ctx, s.store, sqlite.ProjectKind, project.GetId(), current)
	if err != nil {
		return nil, toStatusError("project", project.GetId(), err)
	}

	if project.GetStatus() == reinv1.ProjectStatus_PROJECT_STATUS_UNSPECIFIED {
		project.Status = current.GetStatus()
	}
	if project.GetCreatedTime() == nil {
		project.CreatedTime = current.GetCreatedTime()
	}
	if project.Labels == nil {
		project.Labels = cloneMap(current.GetLabels())
	}
	if project.Labels == nil {
		project.Labels = map[string]string{}
	}
	project.UpdatedTime = timestamppb.Now()

	if _, err := updateStoredProto(ctx, s.store, sqlite.ProjectKind, project.GetId(), record.LockVersion, project); err != nil {
		return nil, status.Errorf(codes.Internal, "update project %q: %v", project.GetId(), err)
	}
	return &reinv1.UpdateProjectResponse{Project: project}, nil
}

type ManagedIssueServer struct {
	reinv1.UnimplementedIssueServiceServer
	store *sqlite.Store
}

func (s *ManagedIssueServer) ListIssues(ctx context.Context, req *reinv1.ListIssuesRequest) (*reinv1.ListIssuesResponse, error) {
	issues, err := listStoredMessages(ctx, s.store, sqlite.IssueKind, func() *reinv1.Issue { return &reinv1.Issue{} })
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list issues: %v", err)
	}

	query := strings.ToLower(strings.TrimSpace(req.GetQuery()))
	filtered := make([]*reinv1.Issue, 0, len(issues))
	for _, issue := range issues {
		if req.GetProjectId() != "" && issue.GetProjectId() != req.GetProjectId() {
			continue
		}
		if req.GetStatus() != reinv1.IssueStatus_ISSUE_STATUS_UNSPECIFIED && issue.GetStatus() != req.GetStatus() {
			continue
		}
		if req.GetAssignee() != "" && issue.GetAssignee() != req.GetAssignee() {
			continue
		}
		if query != "" && !matchesQuery(query, issue.GetId(), issue.GetProjectId(), issue.GetTitle(), issue.GetSummary(), issue.GetWorkflowId(), issue.GetAssignee()) {
			continue
		}
		filtered = append(filtered, issue)
	}

	paged, page, err := paginateResults(filtered, req.GetPage())
	if err != nil {
		return nil, err
	}
	return &reinv1.ListIssuesResponse{Issues: paged, Page: page}, nil
}

func (s *ManagedIssueServer) CreateIssue(ctx context.Context, req *reinv1.CreateIssueRequest) (*reinv1.CreateIssueResponse, error) {
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

func (s *ManagedIssueServer) GetIssue(ctx context.Context, req *reinv1.GetIssueRequest) (*reinv1.GetIssueResponse, error) {
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	issue := &reinv1.Issue{}
	if _, err := loadStoredProto(ctx, s.store, sqlite.IssueKind, req.GetId(), issue); err != nil {
		return nil, toStatusError("issue", req.GetId(), err)
	}
	return &reinv1.GetIssueResponse{Issue: issue}, nil
}

func (s *ManagedIssueServer) UpdateIssue(ctx context.Context, req *reinv1.UpdateIssueRequest) (*reinv1.UpdateIssueResponse, error) {
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

	current := &reinv1.Issue{}
	record, err := loadStoredProto(ctx, s.store, sqlite.IssueKind, issue.GetId(), current)
	if err != nil {
		return nil, toStatusError("issue", issue.GetId(), err)
	}

	if issue.GetStatus() == reinv1.IssueStatus_ISSUE_STATUS_UNSPECIFIED {
		issue.Status = current.GetStatus()
	}
	if issue.GetCreatedTime() == nil {
		issue.CreatedTime = current.GetCreatedTime()
	}
	if issue.Labels == nil {
		issue.Labels = cloneMap(current.GetLabels())
	}
	if issue.Labels == nil {
		issue.Labels = map[string]string{}
	}
	issue.UpdatedTime = timestamppb.Now()

	if _, err := updateStoredProto(ctx, s.store, sqlite.IssueKind, issue.GetId(), record.LockVersion, issue); err != nil {
		return nil, status.Errorf(codes.Internal, "update issue %q: %v", issue.GetId(), err)
	}
	return &reinv1.UpdateIssueResponse{Issue: issue}, nil
}

type ManagedExecutionServer struct {
	reinv1.UnimplementedExecutionServiceServer
	catalog ManagedCatalog
	engine  *workflow.Engine
	store   *sqlite.Store
}

func (s *ManagedExecutionServer) ListExecutions(ctx context.Context, req *reinv1.ListExecutionsRequest) (*reinv1.ListExecutionsResponse, error) {
	executions, err := listStoredMessages(ctx, s.store, sqlite.ExecutionKind, func() *reinv1.Execution { return &reinv1.Execution{} })
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list executions: %v", err)
	}

	filtered := make([]*reinv1.Execution, 0, len(executions))
	for _, execution := range executions {
		if req.GetIssueId() != "" && execution.GetIssueId() != req.GetIssueId() {
			continue
		}
		if req.GetStatus() != reinv1.ExecutionStatus_EXECUTION_STATUS_UNSPECIFIED && execution.GetStatus() != req.GetStatus() {
			continue
		}
		filtered = append(filtered, execution)
	}

	paged, page, err := paginateResults(filtered, req.GetPage())
	if err != nil {
		return nil, err
	}
	return &reinv1.ListExecutionsResponse{Executions: paged, Page: page}, nil
}

func (s *ManagedExecutionServer) StartExecution(ctx context.Context, req *reinv1.StartExecutionRequest) (*reinv1.StartExecutionResponse, error) {
	if strings.TrimSpace(req.GetIssueId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "issue_id is required")
	}
	if strings.TrimSpace(req.GetWorkflowId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "workflow_id is required")
	}

	issue := &reinv1.Issue{}
	issueRecord, err := loadStoredProto(ctx, s.store, sqlite.IssueKind, req.GetIssueId(), issue)
	if err != nil {
		return nil, toStatusError("issue", req.GetIssueId(), err)
	}
	workflowEntity := &reinv1.Workflow{}
	if _, err := loadStoredProto(ctx, s.store, sqlite.WorkflowKind, req.GetWorkflowId(), workflowEntity); err != nil {
		return nil, toStatusError("workflow", req.GetWorkflowId(), err)
	}

	messages := s.engine.Validate(workflowEntity, func(id string) bool {
		_, ok := s.catalog.Lookup(id)
		return ok
	})
	if len(messages) > 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "workflow %q is invalid", workflowEntity.GetId())
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
		Id:          executionID(req.GetIssueId()),
		IssueId:     issue.GetId(),
		WorkflowId:  workflowEntity.GetId(),
		RequestedBy: req.GetRequestedBy(),
		Status:      reinv1.ExecutionStatus_EXECUTION_STATUS_QUEUED,
		CreatedTime: now,
		Metadata:    cloneMap(req.GetInputs()),
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

	firstAdapter, _ := firstWorkflowAdapter(workflowEntity)
	execution.AdapterId = firstAdapter
	execution.Status = reinv1.ExecutionStatus_EXECUTION_STATUS_RUNNING
	execution.StartedTime = now
	executionRecord, err = updateStoredProto(ctx, s.store, sqlite.ExecutionKind, execution.GetId(), executionRecord.LockVersion, execution)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "start execution %q: %v", execution.GetId(), err)
	}

	state := &workflow.RuntimeState{Workflow: workflowEntity, Issue: issue, Execution: execution}
	runner := catalogRunner{catalog: s.catalog}
	if err := s.engine.Run(ctx, state, runner); err != nil {
		execution.Status = reinv1.ExecutionStatus_EXECUTION_STATUS_FAILED
		execution.FinishedTime = timestamppb.Now()
		execution.Metadata["result"] = "failed"
		issue.Status = reinv1.IssueStatus_ISSUE_STATUS_BLOCKED
		issue.UpdatedTime = timestamppb.Now()
		issue.Labels["integration_status"] = "failed"
		if _, updateErr := updateStoredProto(ctx, s.store, sqlite.ExecutionKind, execution.GetId(), executionRecord.LockVersion, execution); updateErr != nil {
			return nil, status.Errorf(codes.Internal, "execution %q failed with %v and could not be stored: %v", execution.GetId(), err, updateErr)
		}
		if _, updateErr := updateStoredProto(ctx, s.store, sqlite.IssueKind, issue.GetId(), issueRecord.LockVersion, issue); updateErr != nil {
			return nil, status.Errorf(codes.Internal, "issue %q failed with %v and could not be stored: %v", issue.GetId(), err, updateErr)
		}
		return nil, status.Errorf(codes.FailedPrecondition, "run workflow %q: %v", workflowEntity.GetId(), err)
	}

	issue.Status = reinv1.IssueStatus_ISSUE_STATUS_RESOLVED
	issue.UpdatedTime = timestamppb.Now()
	if execution.Metadata["result"] == "" {
		execution.Metadata["result"] = "succeeded"
	}
	issue.Labels["integration_status"] = execution.Metadata["result"]
	if issue.Labels["integration_status"] == "succeeded" {
		issue.Labels["integration_status"] = "merged"
	}
	execution.Status = reinv1.ExecutionStatus_EXECUTION_STATUS_SUCCEEDED
	execution.FinishedTime = timestamppb.Now()
	if _, err := updateStoredProto(ctx, s.store, sqlite.IssueKind, issue.GetId(), issueRecord.LockVersion, issue); err != nil {
		return nil, status.Errorf(codes.Internal, "resolve issue %q: %v", issue.GetId(), err)
	}
	if _, err := updateStoredProto(ctx, s.store, sqlite.ExecutionKind, execution.GetId(), executionRecord.LockVersion, execution); err != nil {
		return nil, status.Errorf(codes.Internal, "finish execution %q: %v", execution.GetId(), err)
	}
	return &reinv1.StartExecutionResponse{Execution: execution}, nil
}

func (s *ManagedExecutionServer) GetExecution(ctx context.Context, req *reinv1.GetExecutionRequest) (*reinv1.GetExecutionResponse, error) {
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	execution := &reinv1.Execution{}
	if _, err := loadStoredProto(ctx, s.store, sqlite.ExecutionKind, req.GetId(), execution); err != nil {
		return nil, toStatusError("execution", req.GetId(), err)
	}
	return &reinv1.GetExecutionResponse{Execution: execution}, nil
}

func (s *ManagedExecutionServer) InspectExecution(ctx context.Context, req *reinv1.InspectExecutionRequest) (*reinv1.InspectExecutionResponse, error) {
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	execution := &reinv1.Execution{}
	if _, err := loadStoredProto(ctx, s.store, sqlite.ExecutionKind, req.GetId(), execution); err != nil {
		return nil, toStatusError("execution", req.GetId(), err)
	}

	issue := &reinv1.Issue{}
	if _, err := loadStoredProto(ctx, s.store, sqlite.IssueKind, execution.GetIssueId(), issue); err != nil {
		return nil, toStatusError("issue", execution.GetIssueId(), err)
	}

	workflowEntity := &reinv1.Workflow{}
	if _, err := loadStoredProto(ctx, s.store, sqlite.WorkflowKind, execution.GetWorkflowId(), workflowEntity); err != nil {
		return nil, toStatusError("workflow", execution.GetWorkflowId(), err)
	}

	taskSteps, err := s.engine.ListTaskSteps(ctx, execution.GetId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list execution %q task steps: %v", execution.GetId(), err)
	}
	sideEffects, err := s.engine.ListSideEffects(ctx, execution.GetId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list execution %q side effects: %v", execution.GetId(), err)
	}

	stepIndex := workflowStepIndex(workflowEntity)
	adapters := executionAdapters(s.catalog, workflowEntity)

	return &reinv1.InspectExecutionResponse{
		Execution:    execution,
		Issue:        issue,
		Workflow:     workflowEntity,
		TaskSteps:    executionTaskSteps(taskSteps, stepIndex),
		SideEffects:  executionSideEffects(sideEffects, stepIndex),
		Adapters:     adapters,
		LookingGlass: executionLookingGlass(adapters),
	}, nil
}

func (s *ManagedExecutionServer) CancelExecution(ctx context.Context, req *reinv1.CancelExecutionRequest) (*reinv1.CancelExecutionResponse, error) {
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	execution := &reinv1.Execution{}
	executionRecord, err := loadStoredProto(ctx, s.store, sqlite.ExecutionKind, req.GetId(), execution)
	if err != nil {
		return nil, toStatusError("execution", req.GetId(), err)
	}
	if execution.GetStatus() != reinv1.ExecutionStatus_EXECUTION_STATUS_RUNNING {
		return nil, status.Errorf(codes.FailedPrecondition, "execution %q is not running", execution.GetId())
	}
	issue := &reinv1.Issue{}
	issueRecord, err := loadStoredProto(ctx, s.store, sqlite.IssueKind, execution.GetIssueId(), issue)
	if err != nil {
		return nil, toStatusError("issue", execution.GetIssueId(), err)
	}
	workflowEntity := &reinv1.Workflow{}
	if _, err := loadStoredProto(ctx, s.store, sqlite.WorkflowKind, execution.GetWorkflowId(), workflowEntity); err != nil {
		return nil, toStatusError("workflow", execution.GetWorkflowId(), err)
	}
	state := &workflow.RuntimeState{Workflow: workflowEntity, Issue: issue, Execution: execution}
	if err := s.engine.Cancel(ctx, state, catalogRunner{catalog: s.catalog}, req.GetReason()); err != nil {
		return nil, status.Errorf(codes.Internal, "cancel execution %q: %v", execution.GetId(), err)
	}
	execution.Status = reinv1.ExecutionStatus_EXECUTION_STATUS_CANCELED
	execution.FinishedTime = timestamppb.Now()
	if execution.Metadata == nil {
		execution.Metadata = map[string]string{}
	}
	execution.Metadata["result"] = "canceled"
	execution.Metadata["cancel_reason"] = req.GetReason()
	issue.Status = reinv1.IssueStatus_ISSUE_STATUS_CANCELED
	issue.UpdatedTime = timestamppb.Now()
	if issue.Labels == nil {
		issue.Labels = map[string]string{}
	}
	issue.Labels["integration_status"] = "canceled"
	if _, err := updateStoredProto(ctx, s.store, sqlite.ExecutionKind, execution.GetId(), executionRecord.LockVersion, execution); err != nil {
		return nil, status.Errorf(codes.Internal, "persist canceled execution %q: %v", execution.GetId(), err)
	}
	if _, err := updateStoredProto(ctx, s.store, sqlite.IssueKind, issue.GetId(), issueRecord.LockVersion, issue); err != nil {
		return nil, status.Errorf(codes.Internal, "persist canceled issue %q: %v", issue.GetId(), err)
	}
	return &reinv1.CancelExecutionResponse{Execution: execution}, nil
}

type catalogRunner struct{ catalog ManagedCatalog }

func (r catalogRunner) Run(ctx context.Context, state *workflow.RuntimeState, phase workflow.Phase, direction workflow.Direction, effect *workflow.SideEffect) error {
	adapter, ok := r.catalog.Lookup(phase.AdapterID)
	if !ok {
		return fmt.Errorf("adapter %q not found", phase.AdapterID)
	}
	return adapter.Run(ctx, state, phase, direction, effect)
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
	if err == nil {
		return nil
	}
	if errors.Is(err, sqlite.ErrNotFound) {
		return status.Errorf(codes.NotFound, "%s %q not found", kind, id)
	}
	return status.Errorf(codes.Internal, "%s %q: %v", kind, id, err)
}

func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func listStoredMessages[T proto.Message](ctx context.Context, store *sqlite.Store, kind sqlite.EntityKind, newMessage func() T) ([]T, error) {
	records, err := store.List(ctx, kind, sqlite.ListOptions{})
	if err != nil {
		return nil, err
	}

	messages := make([]T, 0, len(records))
	for _, record := range records {
		message := newMessage()
		if err := protojson.Unmarshal(record.Payload, message); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func paginateResults[T any](items []T, page *reinv1.PageRequest) ([]T, *reinv1.PageResponse, error) {
	offset := 0
	pageSize := 0
	if page != nil {
		if strings.TrimSpace(page.GetPageToken()) != "" {
			value, err := strconv.Atoi(page.GetPageToken())
			if err != nil || value < 0 {
				return nil, nil, status.Error(codes.InvalidArgument, "page.page_token must be a non-negative integer offset")
			}
			offset = value
		}
		pageSize = int(page.GetPageSize())
		if pageSize < 0 {
			return nil, nil, status.Error(codes.InvalidArgument, "page.page_size must be non-negative")
		}
	}

	if offset > len(items) {
		offset = len(items)
	}
	items = items[offset:]
	if pageSize == 0 || pageSize >= len(items) {
		if len(items) == 0 {
			return items, nil, nil
		}
		return items, nil, nil
	}

	nextOffset := offset + pageSize
	return items[:pageSize], &reinv1.PageResponse{NextPageToken: strconv.Itoa(nextOffset)}, nil
}

func matchesQuery(query string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(strings.ToLower(candidate), query) {
			return true
		}
	}
	return false
}

func executionID(issueID string) string {
	return fmt.Sprintf("exec-%s-001", strings.ToLower(issueID))
}

func firstWorkflowAdapter(workflowEntity *reinv1.Workflow) (string, bool) {
	for _, step := range workflowEntity.GetSteps() {
		if strings.TrimSpace(step.GetAdapterId()) != "" {
			return step.GetAdapterId(), true
		}
	}
	return "", false
}

func workflowStepIndex(workflowEntity *reinv1.Workflow) map[string]*reinv1.WorkflowStep {
	index := make(map[string]*reinv1.WorkflowStep, len(workflowEntity.GetSteps()))
	for _, step := range workflowEntity.GetSteps() {
		if step == nil {
			continue
		}
		index[step.GetId()] = step
	}
	return index
}

func executionTaskSteps(steps []workflow.TaskStep, index map[string]*reinv1.WorkflowStep) []*reinv1.ExecutionTaskStep {
	out := make([]*reinv1.ExecutionTaskStep, 0, len(steps))
	for _, step := range steps {
		workflowStep := index[step.PhaseID]
		out = append(out, &reinv1.ExecutionTaskStep{
			PhaseId:   step.PhaseID,
			PhaseName: workflowStepName(workflowStep, step.PhaseID),
			AdapterId: workflowStepAdapterID(workflowStep),
			Lane:      step.Lane,
			Direction: string(step.Direction),
			Operation: step.Operation,
			Status:    string(step.Status),
			Sequence:  int32(step.Sequence),
			Error:     step.Error,
		})
	}
	return out
}

func executionSideEffects(effects []workflow.SideEffect, index map[string]*reinv1.WorkflowStep) []*reinv1.ExecutionSideEffect {
	out := make([]*reinv1.ExecutionSideEffect, 0, len(effects))
	for _, effect := range effects {
		workflowStep := index[effect.PhaseID]
		out = append(out, &reinv1.ExecutionSideEffect{
			PhaseId:     effect.PhaseID,
			PhaseName:   workflowStepName(workflowStep, effect.PhaseID),
			AdapterId:   workflowStepAdapterID(workflowStep),
			Lane:        effect.Lane,
			Direction:   string(effect.Direction),
			Operation:   effect.Operation,
			Status:      string(effect.Status),
			Sequence:    int32(effect.Sequence),
			Reason:      effect.Reason,
			TargetPhase: effect.TargetPhase,
			Inputs:      cloneMap(effect.Inputs),
			Outputs:     cloneMap(effect.Outputs),
			Error:       effect.Error,
		})
	}
	return out
}

func executionAdapters(catalog ManagedCatalog, workflowEntity *reinv1.Workflow) []*reinv1.Adapter {
	if catalog == nil || workflowEntity == nil {
		return nil
	}

	seen := map[string]struct{}{}
	ids := make([]string, 0, len(workflowEntity.GetSteps()))
	for _, step := range workflowEntity.GetSteps() {
		adapterID := strings.TrimSpace(step.GetAdapterId())
		if adapterID == "" {
			continue
		}
		if _, ok := seen[adapterID]; ok {
			continue
		}
		seen[adapterID] = struct{}{}
		ids = append(ids, adapterID)
	}
	slices.Sort(ids)

	adapters := make([]*reinv1.Adapter, 0, len(ids))
	for _, id := range ids {
		if adapter, ok := catalog.Lookup(id); ok {
			adapters = append(adapters, adapter.Descriptor())
		}
	}
	return adapters
}

func executionLookingGlass(adapters []*reinv1.Adapter) *reinv1.LookingGlassState {
	tailAdapters := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		if adapter == nil {
			continue
		}
		if strings.EqualFold(adapter.GetCapabilities()["tail"], "true") {
			tailAdapters = append(tailAdapters, adapter.GetId())
		}
	}
	slices.Sort(tailAdapters)
	if len(tailAdapters) == 0 {
		return &reinv1.LookingGlassState{
			Status: "No adapters in this execution advertise looking-glass tail support.",
		}
	}
	return &reinv1.LookingGlassState{
		Supported:  true,
		Available:  false,
		AdapterIds: tailAdapters,
		Status:     "Adapters advertise tail support, but the daemon does not expose looking-glass streaming yet.",
	}
}

func workflowStepName(step *reinv1.WorkflowStep, fallback string) string {
	if step == nil || strings.TrimSpace(step.GetName()) == "" {
		return fallback
	}
	return step.GetName()
}

func workflowStepAdapterID(step *reinv1.WorkflowStep) string {
	if step == nil {
		return ""
	}
	return step.GetAdapterId()
}
