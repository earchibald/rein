package server

import (
	"context"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/service"
	"github.com/earchibald/rein/internal/storage/sqlite"
)

type grpcContract struct {
	service    string
	method     string
	fullMethod string
	input      protoreflect.FullName
	output     protoreflect.FullName
	newRequest func() proto.Message
}

func TestRuntimeGRPCServiceContracts(t *testing.T) {
	t.Parallel()

	runtime := New(Options{})
	serviceInfo := runtime.GRPC().GetServiceInfo()

	wantByService := map[string][]string{}
	for _, contract := range allGRPCContracts() {
		wantByService[contract.service] = append(wantByService[contract.service], contract.method)

		methodDescriptor := findMethodDescriptor(t, contract.fullMethod)
		if got := string(methodDescriptor.Input().FullName()); got != string(contract.input) {
			t.Fatalf("%s input = %s, want %s", contract.fullMethod, methodDescriptor.Input().FullName(), contract.input)
		}
		if got := string(methodDescriptor.Output().FullName()); got != string(contract.output) {
			t.Fatalf("%s output = %s, want %s", contract.fullMethod, methodDescriptor.Output().FullName(), contract.output)
		}
		if methodDescriptor.IsStreamingClient() || methodDescriptor.IsStreamingServer() {
			t.Fatalf("%s unexpectedly declared as streaming", contract.fullMethod)
		}
	}

	if len(serviceInfo) != len(wantByService) {
		t.Fatalf("registered service count = %d, want %d", len(serviceInfo), len(wantByService))
	}

	for serviceName, wantMethods := range wantByService {
		info, ok := serviceInfo[serviceName]
		if !ok {
			t.Fatalf("registered services missing %q", serviceName)
		}

		gotMethods := make([]string, 0, len(info.Methods))
		for _, method := range info.Methods {
			if method.IsClientStream || method.IsServerStream {
				t.Fatalf("%s/%s unexpectedly registered as streaming", serviceName, method.Name)
			}
			gotMethods = append(gotMethods, method.Name)
		}

		slices.Sort(gotMethods)
		slices.Sort(wantMethods)
		if !slices.Equal(gotMethods, wantMethods) {
			t.Fatalf("%s methods = %v, want %v", serviceName, gotMethods, wantMethods)
		}
	}
}

func TestRuntimeUnaryRPCErrorContracts(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		observed = map[string]reflect.Type{}
	)

	runtime := New(Options{
		Services: managedContractServices(t),
		GRPCOptions: []grpc.ServerOption{
			grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				mu.Lock()
				observed[info.FullMethod] = reflect.TypeOf(req)
				mu.Unlock()
				return handler(ctx, req)
			}),
		},
	})

	listener := bufconn.Listen(1024 * 1024)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runtime.Serve(ctx, listener)
	}()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	defer conn.Close()

	for _, contract := range allGRPCContracts() {
		contract := contract
		t.Run(strings.TrimPrefix(strings.ReplaceAll(contract.fullMethod, "/", "_"), "_"), func(t *testing.T) {
			rpcCtx, rpcCancel := context.WithTimeout(context.Background(), time.Second)
			defer rpcCancel()

			reply := emptyReply(t, contract.output)
			err := conn.Invoke(rpcCtx, contract.fullMethod, contract.newRequest(), reply)
			expectation := unaryExpectation(contract)
			if expectation.code == codes.OK {
				if err != nil {
					t.Fatalf("%s error = %v, want nil", contract.fullMethod, err)
				}
			} else {
				if err == nil {
					t.Fatal("Invoke() error = nil, want non-nil")
				}

				st, ok := status.FromError(err)
				if !ok {
					t.Fatalf("status.FromError(%v) = !ok", err)
				}
				if st.Code() != expectation.code {
					t.Fatalf("%s code = %v, want %v", contract.fullMethod, st.Code(), expectation.code)
				}
				if st.Message() != expectation.message {
					t.Fatalf("%s message = %q, want %q", contract.fullMethod, st.Message(), expectation.message)
				}
			}

			mu.Lock()
			gotRequestType := observed[contract.fullMethod]
			mu.Unlock()
			if gotRequestType != reflect.TypeOf(contract.newRequest()) {
				t.Fatalf("%s request type = %v, want %v", contract.fullMethod, gotRequestType, reflect.TypeOf(contract.newRequest()))
			}
		})
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}

type unaryRPCExpectation struct {
	code    codes.Code
	message string
}

func unaryExpectation(contract grpcContract) unaryRPCExpectation {
	switch contract.fullMethod {
	case reinv1.AdapterService_ListAdapters_FullMethodName:
		return unaryRPCExpectation{code: codes.OK}
	case reinv1.AdapterService_GetAdapter_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "id is required"}
	case reinv1.AdapterService_ValidateAdapter_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "adapter is required"}
	case reinv1.ExecutionService_ListExecutions_FullMethodName:
		return unaryRPCExpectation{code: codes.OK}
	case reinv1.ExecutionService_GetExecution_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "id is required"}
	case reinv1.ExecutionService_StartExecution_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "issue_id is required"}
	case reinv1.ExecutionService_CancelExecution_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "id is required"}
	case reinv1.IssueService_ListIssues_FullMethodName:
		return unaryRPCExpectation{code: codes.OK}
	case reinv1.IssueService_GetIssue_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "id is required"}
	case reinv1.IssueService_CreateIssue_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "issue is required"}
	case reinv1.IssueService_UpdateIssue_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "issue is required"}
	case reinv1.ProjectService_ListProjects_FullMethodName:
		return unaryRPCExpectation{code: codes.OK}
	case reinv1.ProjectService_GetProject_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "id is required"}
	case reinv1.ProjectService_CreateProject_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "project is required"}
	case reinv1.ProjectService_UpdateProject_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "project is required"}
	case reinv1.WorkflowService_ListWorkflows_FullMethodName:
		return unaryRPCExpectation{code: codes.OK}
	case reinv1.WorkflowService_GetWorkflow_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "id is required"}
	case reinv1.WorkflowService_ValidateWorkflow_FullMethodName:
		return unaryRPCExpectation{code: codes.InvalidArgument, message: "workflow is required"}
	default:
		return unaryRPCExpectation{
			code:    codes.Unimplemented,
			message: "method " + contract.method + " not implemented",
		}
	}
}

func managedContractServices(t testing.TB) service.Set {
	t.Helper()

	store, err := sqlite.OpenInMemoryAndMigrate(context.Background(), t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	return service.NewManagedSet(store, newRuntimeCatalog())
}

func findMethodDescriptor(t testing.TB, fullMethod string) protoreflect.MethodDescriptor {
	t.Helper()

	parts := strings.Split(strings.TrimPrefix(fullMethod, "/"), "/")
	if len(parts) != 2 {
		t.Fatalf("full method %q must be /service/method", fullMethod)
	}

	serviceDescriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(parts[0]))
	if err != nil {
		t.Fatalf("FindDescriptorByName(%q) error = %v", parts[0], err)
	}

	service, ok := serviceDescriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		t.Fatalf("descriptor %q is %T, want service descriptor", parts[0], serviceDescriptor)
	}

	method := service.Methods().ByName(protoreflect.Name(parts[1]))
	if method == nil {
		t.Fatalf("service %q missing method %q", parts[0], parts[1])
	}

	return method
}

func emptyReply(t testing.TB, fullName protoreflect.FullName) proto.Message {
	t.Helper()

	descriptor, err := protoregistry.GlobalTypes.FindMessageByName(fullName)
	if err != nil {
		t.Fatalf("FindMessageByName(%q) error = %v", fullName, err)
	}

	return descriptor.New().Interface()
}

func allGRPCContracts() []grpcContract {
	return []grpcContract{
		{
			service:    "rein.v1.AdapterService",
			method:     "ListAdapters",
			fullMethod: reinv1.AdapterService_ListAdapters_FullMethodName,
			input:      "rein.v1.ListAdaptersRequest",
			output:     "rein.v1.ListAdaptersResponse",
			newRequest: func() proto.Message { return &reinv1.ListAdaptersRequest{} },
		},
		{
			service:    "rein.v1.AdapterService",
			method:     "GetAdapter",
			fullMethod: reinv1.AdapterService_GetAdapter_FullMethodName,
			input:      "rein.v1.GetAdapterRequest",
			output:     "rein.v1.GetAdapterResponse",
			newRequest: func() proto.Message { return &reinv1.GetAdapterRequest{} },
		},
		{
			service:    "rein.v1.AdapterService",
			method:     "ValidateAdapter",
			fullMethod: reinv1.AdapterService_ValidateAdapter_FullMethodName,
			input:      "rein.v1.ValidateAdapterRequest",
			output:     "rein.v1.ValidateAdapterResponse",
			newRequest: func() proto.Message { return &reinv1.ValidateAdapterRequest{} },
		},
		{
			service:    "rein.v1.ExecutionService",
			method:     "ListExecutions",
			fullMethod: reinv1.ExecutionService_ListExecutions_FullMethodName,
			input:      "rein.v1.ListExecutionsRequest",
			output:     "rein.v1.ListExecutionsResponse",
			newRequest: func() proto.Message { return &reinv1.ListExecutionsRequest{} },
		},
		{
			service:    "rein.v1.ExecutionService",
			method:     "GetExecution",
			fullMethod: reinv1.ExecutionService_GetExecution_FullMethodName,
			input:      "rein.v1.GetExecutionRequest",
			output:     "rein.v1.GetExecutionResponse",
			newRequest: func() proto.Message { return &reinv1.GetExecutionRequest{} },
		},
		{
			service:    "rein.v1.ExecutionService",
			method:     "StartExecution",
			fullMethod: reinv1.ExecutionService_StartExecution_FullMethodName,
			input:      "rein.v1.StartExecutionRequest",
			output:     "rein.v1.StartExecutionResponse",
			newRequest: func() proto.Message { return &reinv1.StartExecutionRequest{} },
		},
		{
			service:    "rein.v1.ExecutionService",
			method:     "CancelExecution",
			fullMethod: reinv1.ExecutionService_CancelExecution_FullMethodName,
			input:      "rein.v1.CancelExecutionRequest",
			output:     "rein.v1.CancelExecutionResponse",
			newRequest: func() proto.Message { return &reinv1.CancelExecutionRequest{} },
		},
		{
			service:    "rein.v1.IssueService",
			method:     "ListIssues",
			fullMethod: reinv1.IssueService_ListIssues_FullMethodName,
			input:      "rein.v1.ListIssuesRequest",
			output:     "rein.v1.ListIssuesResponse",
			newRequest: func() proto.Message { return &reinv1.ListIssuesRequest{} },
		},
		{
			service:    "rein.v1.IssueService",
			method:     "GetIssue",
			fullMethod: reinv1.IssueService_GetIssue_FullMethodName,
			input:      "rein.v1.GetIssueRequest",
			output:     "rein.v1.GetIssueResponse",
			newRequest: func() proto.Message { return &reinv1.GetIssueRequest{} },
		},
		{
			service:    "rein.v1.IssueService",
			method:     "CreateIssue",
			fullMethod: reinv1.IssueService_CreateIssue_FullMethodName,
			input:      "rein.v1.CreateIssueRequest",
			output:     "rein.v1.CreateIssueResponse",
			newRequest: func() proto.Message { return &reinv1.CreateIssueRequest{} },
		},
		{
			service:    "rein.v1.IssueService",
			method:     "UpdateIssue",
			fullMethod: reinv1.IssueService_UpdateIssue_FullMethodName,
			input:      "rein.v1.UpdateIssueRequest",
			output:     "rein.v1.UpdateIssueResponse",
			newRequest: func() proto.Message { return &reinv1.UpdateIssueRequest{} },
		},
		{
			service:    "rein.v1.ProjectService",
			method:     "ListProjects",
			fullMethod: reinv1.ProjectService_ListProjects_FullMethodName,
			input:      "rein.v1.ListProjectsRequest",
			output:     "rein.v1.ListProjectsResponse",
			newRequest: func() proto.Message { return &reinv1.ListProjectsRequest{} },
		},
		{
			service:    "rein.v1.ProjectService",
			method:     "GetProject",
			fullMethod: reinv1.ProjectService_GetProject_FullMethodName,
			input:      "rein.v1.GetProjectRequest",
			output:     "rein.v1.GetProjectResponse",
			newRequest: func() proto.Message { return &reinv1.GetProjectRequest{} },
		},
		{
			service:    "rein.v1.ProjectService",
			method:     "CreateProject",
			fullMethod: reinv1.ProjectService_CreateProject_FullMethodName,
			input:      "rein.v1.CreateProjectRequest",
			output:     "rein.v1.CreateProjectResponse",
			newRequest: func() proto.Message { return &reinv1.CreateProjectRequest{} },
		},
		{
			service:    "rein.v1.ProjectService",
			method:     "UpdateProject",
			fullMethod: reinv1.ProjectService_UpdateProject_FullMethodName,
			input:      "rein.v1.UpdateProjectRequest",
			output:     "rein.v1.UpdateProjectResponse",
			newRequest: func() proto.Message { return &reinv1.UpdateProjectRequest{} },
		},
		{
			service:    "rein.v1.WorkflowService",
			method:     "ListWorkflows",
			fullMethod: reinv1.WorkflowService_ListWorkflows_FullMethodName,
			input:      "rein.v1.ListWorkflowsRequest",
			output:     "rein.v1.ListWorkflowsResponse",
			newRequest: func() proto.Message { return &reinv1.ListWorkflowsRequest{} },
		},
		{
			service:    "rein.v1.WorkflowService",
			method:     "GetWorkflow",
			fullMethod: reinv1.WorkflowService_GetWorkflow_FullMethodName,
			input:      "rein.v1.GetWorkflowRequest",
			output:     "rein.v1.GetWorkflowResponse",
			newRequest: func() proto.Message { return &reinv1.GetWorkflowRequest{} },
		},
		{
			service:    "rein.v1.WorkflowService",
			method:     "ValidateWorkflow",
			fullMethod: reinv1.WorkflowService_ValidateWorkflow_FullMethodName,
			input:      "rein.v1.ValidateWorkflowRequest",
			output:     "rein.v1.ValidateWorkflowResponse",
			newRequest: func() proto.Message { return &reinv1.ValidateWorkflowRequest{} },
		},
	}
}
