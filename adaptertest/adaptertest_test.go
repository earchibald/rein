package adaptertest

import (
	"context"
	"reflect"
	"testing"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
)

type codingAgentContract interface {
	Run(context.Context, string) error
}

type reviewAgentContract interface {
	Review(context.Context, string) error
}

type codingAgent struct{}

func (*codingAgent) Run(context.Context, string) error {
	return nil
}

type reviewAgent struct{}

func (*reviewAgent) Review(context.Context, string) error {
	return nil
}

func TestRunCodingAgent(t *testing.T) {
	t.Parallel()

	validated := false
	RunCodingAgent(t, Spec{
		Descriptor: &reinv1.Adapter{
			Id:       "copilot",
			Name:     "Copilot",
			Version:  "1.0.0",
			Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
			Capabilities: map[string]string{
				"apply_patch": "true",
			},
		},
		Implementation:       &codingAgent{},
		Contract:             (*codingAgentContract)(nil),
		RequiredCapabilities: []string{"apply_patch"},
		Validate: func(tb testing.TB, descriptor *reinv1.Adapter, implementation any) {
			tb.Helper()
			validated = true
			if descriptor.GetId() != "copilot" {
				tb.Fatalf("descriptor id = %q, want %q", descriptor.GetId(), "copilot")
			}
			if _, ok := implementation.(*codingAgent); !ok {
				tb.Fatalf("implementation type = %T, want *codingAgent", implementation)
			}
		},
	})

	if !validated {
		t.Fatal("Validate callback was not invoked")
	}
}

func TestValidateAcceptsReflectTypeContracts(t *testing.T) {
	t.Parallel()

	err := validate(Spec{
		Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT,
		Descriptor: &reinv1.Adapter{
			Id:       "review-bot",
			Name:     "Review Bot",
			Version:  "1.0.0",
			Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT,
		},
		Implementation: &reviewAgent{},
		Contract:       reflect.TypeOf((*reviewAgentContract)(nil)).Elem(),
	})
	if err != nil {
		t.Fatalf("validate() error = %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec Spec
		want string
	}{
		{
			name: "category mismatch",
			spec: Spec{
				Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
				Descriptor: &reinv1.Adapter{
					Id:       "tracker",
					Name:     "Tracker",
					Version:  "1.0.0",
					Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_TRACKER,
				},
				Implementation: &codingAgent{},
				Contract:       (*codingAgentContract)(nil),
			},
			want: "descriptor category = ADAPTER_CATEGORY_TRACKER, want ADAPTER_CATEGORY_CODING_AGENT",
		},
		{
			name: "missing capability",
			spec: Spec{
				Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
				Descriptor: &reinv1.Adapter{
					Id:       "copilot",
					Name:     "Copilot",
					Version:  "1.0.0",
					Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
				},
				Implementation:       &codingAgent{},
				Contract:             (*codingAgentContract)(nil),
				RequiredCapabilities: []string{"apply_patch"},
			},
			want: "descriptor.capabilities missing apply_patch",
		},
		{
			name: "implementation mismatch",
			spec: Spec{
				Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
				Descriptor: &reinv1.Adapter{
					Id:       "copilot",
					Name:     "Copilot",
					Version:  "1.0.0",
					Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
				},
				Implementation: &reviewAgent{},
				Contract:       (*codingAgentContract)(nil),
			},
			want: "implementation *adaptertest.reviewAgent does not implement adaptertest.codingAgentContract",
		},
		{
			name: "invalid contract",
			spec: Spec{
				Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
				Descriptor: &reinv1.Adapter{
					Id:       "copilot",
					Name:     "Copilot",
					Version:  "1.0.0",
					Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
				},
				Implementation: &codingAgent{},
				Contract:       codingAgent{},
			},
			want: "contract must be a typed nil pointer to an interface or a reflect.Type, got adaptertest.codingAgent",
		},
		{
			name: "missing descriptor metadata",
			spec: Spec{
				Category:       reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
				Descriptor:     &reinv1.Adapter{Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT},
				Implementation: &codingAgent{},
				Contract:       (*codingAgentContract)(nil),
			},
			want: "descriptor.id is required",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validate(tt.spec)
			if err == nil {
				t.Fatal("validate() error = nil, want non-nil")
			}
			if err.Error() != tt.want {
				t.Fatalf("validate() error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}
