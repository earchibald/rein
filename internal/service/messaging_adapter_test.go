package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/adapter"
	"github.com/earchibald/rein/internal/storage/sqlite"
	"github.com/earchibald/rein/internal/workflow"

	"github.com/earchibald/rein/adaptertest"
)

func TestManagedMessagingNullAdapterConformance(t *testing.T) {
	t.Parallel()

	messaging := newManagedMessagingAdapter(managedMessagingNullDescriptor(), nil)
	adaptertest.RunNotification(t, adaptertest.Spec{
		Descriptor:           messaging.Descriptor(),
		Implementation:       messaging,
		Contract:             (*MessagingAdapter)(nil),
		RequiredCapabilities: []string{"messaging.post"},
		Validate: func(tb testing.TB, _ *reinv1.Adapter, implementation any) {
			tb.Helper()

			messagingAdapter, ok := implementation.(MessagingAdapter)
			if !ok {
				tb.Fatalf("implementation type = %T, want MessagingAdapter", implementation)
			}
			receipt, err := messagingAdapter.Post(context.Background(), MessagingPost{Operation: "post"})
			if err != nil {
				tb.Fatalf("Post() error = %v", err)
			}
			if got, want := receipt.Delivery, "noop"; got != want {
				tb.Fatalf("Post() delivery = %q, want %q", got, want)
			}
		},
	})
}

func TestManagedMessagingAdapterRunUsesMessagingSeam(t *testing.T) {
	t.Parallel()

	poster := &capturingMessagingAdapter{
		receipt: &MessagingReceipt{
			Delivery: "queued",
			Metadata: map[string]string{"target": "release-room"},
		},
	}
	messaging := newManagedMessagingAdapter(managedMessagingNullDescriptor(), poster)
	state := &workflow.RuntimeState{
		Workflow:  &reinv1.Workflow{Id: "workflow-release"},
		Issue:     &reinv1.Issue{Id: "RN-24"},
		Execution: &reinv1.Execution{Id: "exec-rn-24-001"},
	}
	phase := workflow.Phase{
		ID:        "notify",
		AdapterID: messagingNullAdapterID,
		Operation: "post",
		Inputs:    map[string]string{"channel": "release-room", "text": "ready"},
	}
	effect := &workflow.SideEffect{
		WorkflowID:  state.Workflow.GetId(),
		IssueID:     state.Issue.GetId(),
		ExecutionID: state.Execution.GetId(),
		Operation:   "post",
		Inputs:      map[string]string{"channel": "release-room", "text": "ready"},
	}

	if err := messaging.Run(context.Background(), state, phase, workflow.DirectionForward, effect); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := poster.post.WorkflowID, state.Workflow.GetId(); got != want {
		t.Fatalf("Post() workflow_id = %q, want %q", got, want)
	}
	if got, want := poster.post.Operation, "post"; got != want {
		t.Fatalf("Post() operation = %q, want %q", got, want)
	}
	if got, want := poster.post.Inputs["channel"], "release-room"; got != want {
		t.Fatalf("Post() channel = %q, want %q", got, want)
	}
	if got, want := effect.Outputs["delivery"], "queued"; got != want {
		t.Fatalf("effect output delivery = %q, want %q", got, want)
	}
	if got, want := effect.Outputs["target"], "release-room"; got != want {
		t.Fatalf("effect output target = %q, want %q", got, want)
	}
}

func TestManagedExecutionServerStartsWorkflowWithMessagingNullAdapter(t *testing.T) {
	t.Parallel()

	root := writeManagedMessagingFixture(t)
	catalog, err := NewManagedCatalogFromRoot(root, adapter.LocalDiscoveryOptions())
	if err != nil {
		t.Fatalf("NewManagedCatalogFromRoot() error = %v", err)
	}

	ctx := context.Background()
	store, err := sqlite.OpenInMemoryAndMigrate(ctx, t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})

	issue := &reinv1.Issue{
		Id:         "RN-24",
		ProjectId:  "rein",
		Title:      "Messaging stub",
		WorkflowId: "workflow-messaging-null",
	}
	definition := &reinv1.Workflow{
		Id:      "workflow-messaging-null",
		Name:    "Messaging null workflow",
		Version: "v1",
		Steps: []*reinv1.WorkflowStep{
			{
				Id:        "notify",
				Name:      "Notify",
				AdapterId: messagingNullAdapterID,
				Inputs: map[string]string{
					workflow.InputOperation: "post",
					"channel":               "release-room",
				},
			},
		},
	}
	if _, err := createStoredProto(ctx, store, sqlite.IssueKind, issue.GetId(), issue); err != nil {
		t.Fatalf("createStoredProto(issue) error = %v", err)
	}
	if _, err := createStoredProto(ctx, store, sqlite.WorkflowKind, definition.GetId(), definition); err != nil {
		t.Fatalf("createStoredProto(workflow) error = %v", err)
	}

	server := &ManagedExecutionServer{
		catalog: catalog,
		engine:  workflow.New(store),
		store:   store,
	}
	resp, err := server.StartExecution(ctx, &reinv1.StartExecutionRequest{
		IssueId:     issue.GetId(),
		WorkflowId:  definition.GetId(),
		RequestedBy: "copilot",
	})
	if err != nil {
		t.Fatalf("StartExecution() error = %v", err)
	}
	if got, want := resp.GetExecution().GetStatus(), reinv1.ExecutionStatus_EXECUTION_STATUS_SUCCEEDED; got != want {
		t.Fatalf("StartExecution() status = %s, want %s", got, want)
	}

	effects, err := server.engine.ListSideEffects(ctx, resp.GetExecution().GetId())
	if err != nil {
		t.Fatalf("ListSideEffects() error = %v", err)
	}
	if len(effects) != 1 {
		t.Fatalf("ListSideEffects() len = %d, want 1", len(effects))
	}
	if got, want := effects[0].Outputs["delivery"], "noop"; got != want {
		t.Fatalf("side effect delivery = %q, want %q", got, want)
	}
}

type capturingMessagingAdapter struct {
	post    MessagingPost
	receipt *MessagingReceipt
	err     error
}

func (a *capturingMessagingAdapter) Post(_ context.Context, post MessagingPost) (*MessagingReceipt, error) {
	a.post = post
	return a.receipt, a.err
}

func writeManagedMessagingFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	mustServiceMkdirAll(t, filepath.Join(root, ".claude-plugin"))
	writeServiceManifest(t, root, messagingNullAdapterID, map[string]any{
		"name":             messagingNullAdapterID,
		"version":          "0.1.0",
		"description":      "No-op managed messaging adapter stub.",
		"category":         "messaging",
		"daemonApiVersion": adapter.CurrentDaemonAPIVersion,
		"capabilities": map[string]string{
			"messaging.post": "true",
		},
	})

	document := map[string]any{
		"name": "rein-managed-messaging-fixture",
		"plugins": []any{
			map[string]any{
				"name":             messagingNullAdapterID,
				"source":           "./plugins/messaging-null",
				"version":          "0.1.0",
				"description":      "No-op managed messaging adapter stub.",
				"category":         "messaging",
				"daemonApiVersion": adapter.CurrentDaemonAPIVersion,
			},
		},
	}

	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile(marketplace.json) error = %v", err)
	}
	return root
}
