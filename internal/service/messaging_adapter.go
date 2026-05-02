package service

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/workflow"
)

const messagingNullAdapterID = "messaging-null"

// MessagingAdapter is the minimal managed messaging seam for future Slack and
// Discord adapters.
type MessagingAdapter interface {
	Post(context.Context, MessagingPost) (*MessagingReceipt, error)
}

type MessagingPost struct {
	WorkflowID  string
	IssueID     string
	ExecutionID string
	PhaseID     string
	AdapterID   string
	Operation   string
	Direction   workflow.Direction
	Inputs      map[string]string
}

type MessagingReceipt struct {
	Delivery string
	Metadata map[string]string
}

type managedMessagingAdapter struct {
	descriptor *reinv1.Adapter
	poster     MessagingAdapter
}

func newManagedMessagingAdapter(descriptor *reinv1.Adapter, poster MessagingAdapter) *managedMessagingAdapter {
	if descriptor == nil {
		descriptor = managedMessagingNullDescriptor()
	}
	if poster == nil {
		poster = nullMessagingAdapter{}
	}
	return &managedMessagingAdapter{
		descriptor: proto.Clone(descriptor).(*reinv1.Adapter),
		poster:     poster,
	}
}

func managedMessagingNullDescriptor() *reinv1.Adapter {
	return &reinv1.Adapter{
		Id:          messagingNullAdapterID,
		Name:        "Messaging Null",
		Category:    reinv1.AdapterCategory_ADAPTER_CATEGORY_NOTIFICATION,
		Description: "No-op managed messaging adapter stub for workflows until Slack and Discord adapters land.",
		Version:     "0.1.0",
		Enabled:     true,
		Capabilities: map[string]string{
			"messaging.post": "true",
		},
	}
}

func (a *managedMessagingAdapter) Descriptor() *reinv1.Adapter {
	if a == nil || a.descriptor == nil {
		return nil
	}
	return proto.Clone(a.descriptor).(*reinv1.Adapter)
}

func (a *managedMessagingAdapter) Post(ctx context.Context, post MessagingPost) (*MessagingReceipt, error) {
	if a == nil || a.descriptor == nil {
		return nil, fmt.Errorf("adapter execution is not configured")
	}
	if a.poster == nil {
		return nil, fmt.Errorf("messaging adapter %q does not support posting", a.descriptor.GetId())
	}
	return a.poster.Post(ctx, post)
}

func (a *managedMessagingAdapter) Run(ctx context.Context, state *workflow.RuntimeState, phase workflow.Phase, direction workflow.Direction, effect *workflow.SideEffect) error {
	receipt, err := a.Post(ctx, newMessagingPost(state, phase, direction, effect))
	if err != nil || receipt == nil || effect == nil {
		return err
	}

	if effect.Outputs == nil {
		effect.Outputs = map[string]string{}
	}
	if receipt.Delivery != "" {
		effect.Outputs["delivery"] = receipt.Delivery
	}
	for key, value := range receipt.Metadata {
		effect.Outputs[key] = value
	}
	return nil
}

func newMessagingPost(state *workflow.RuntimeState, phase workflow.Phase, direction workflow.Direction, effect *workflow.SideEffect) MessagingPost {
	post := MessagingPost{
		PhaseID:   phase.ID,
		AdapterID: phase.AdapterID,
		Operation: phase.Operation,
		Direction: direction,
		Inputs:    cloneMap(phase.Inputs),
	}
	if state != nil {
		if state.Workflow != nil {
			post.WorkflowID = state.Workflow.GetId()
		}
		if state.Issue != nil {
			post.IssueID = state.Issue.GetId()
		}
		if state.Execution != nil {
			post.ExecutionID = state.Execution.GetId()
		}
	}
	if effect == nil {
		return post
	}
	if effect.WorkflowID != "" {
		post.WorkflowID = effect.WorkflowID
	}
	if effect.IssueID != "" {
		post.IssueID = effect.IssueID
	}
	if effect.ExecutionID != "" {
		post.ExecutionID = effect.ExecutionID
	}
	if effect.Operation != "" {
		post.Operation = effect.Operation
	}
	if len(effect.Inputs) > 0 {
		post.Inputs = cloneMap(effect.Inputs)
	}
	return post
}

type nullMessagingAdapter struct{}

func (nullMessagingAdapter) Post(context.Context, MessagingPost) (*MessagingReceipt, error) {
	return &MessagingReceipt{Delivery: "noop"}, nil
}
