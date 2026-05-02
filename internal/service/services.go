package service

import (
	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/adapter"
)

// Set contains the in-process gRPC service implementations registered by the
// server runtime.
type Set struct {
	Adapter   reinv1.AdapterServiceServer
	Execution reinv1.ExecutionServiceServer
	Issue     reinv1.IssueServiceServer
	Project   reinv1.ProjectServiceServer
	Workflow  reinv1.WorkflowServiceServer
}

func NewSet() Set {
	adapterServer, _ := NewAdapterServerFromRoot(".", adapter.DiscoveryOptions{})
	return Set{
		Adapter:   adapterServer,
		Execution: ExecutionServer{},
		Issue:     IssueServer{},
		Project:   ProjectServer{},
		Workflow:  WorkflowServer{},
	}
}

func NewSetFromRoot(root string, options adapter.DiscoveryOptions) (Set, error) {
	adapterServer, err := NewAdapterServerFromRoot(root, options)
	return Set{
		Adapter:   adapterServer,
		Execution: ExecutionServer{},
		Issue:     IssueServer{},
		Project:   ProjectServer{},
		Workflow:  WorkflowServer{},
	}, err
}

func (s Set) WithDefaults() Set {
	if s.Adapter == nil {
		adapterServer, _ := NewAdapterServerFromRoot(".", adapter.DiscoveryOptions{})
		s.Adapter = adapterServer
	}
	if s.Execution == nil {
		s.Execution = ExecutionServer{}
	}
	if s.Issue == nil {
		s.Issue = IssueServer{}
	}
	if s.Project == nil {
		s.Project = ProjectServer{}
	}
	if s.Workflow == nil {
		s.Workflow = WorkflowServer{}
	}

	return s
}

type ExecutionServer struct {
	reinv1.UnimplementedExecutionServiceServer
}

type IssueServer struct {
	reinv1.UnimplementedIssueServiceServer
}

type ProjectServer struct {
	reinv1.UnimplementedProjectServiceServer
}

type WorkflowServer struct {
	reinv1.UnimplementedWorkflowServiceServer
}
