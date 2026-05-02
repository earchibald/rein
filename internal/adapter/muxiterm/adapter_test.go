package muxiterm

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/earchibald/rein/adaptertest"
	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/workflow"
)

type multiplexerContract interface {
	Descriptor() *reinv1.Adapter
	Run(context.Context, *workflow.RuntimeState, workflow.Phase, workflow.Direction, *workflow.SideEffect) error
}

func TestAdapterConformance(t *testing.T) {
	t.Parallel()

	adaptertest.RunMultiplexer(t, adaptertest.Spec{
		Descriptor:           DefaultDescriptor(),
		Implementation:       NewWithExecutor("muxiterm", &fakeExecutor{}),
		Contract:             (*multiplexerContract)(nil),
		RequiredCapabilities: []string{"session.attach", "pane.split", "input.send", "tail"},
	})
}

func TestRunExecutesMuxitermAndCapturesOutputs(t *testing.T) {
	t.Parallel()

	executor := &fakeExecutor{
		result: Result{
			Stdout: `{"ok":true,"session":"dev","focused":true,"panes":["left","right"]}`,
			Stderr: "debug trace",
		},
	}
	adapter := NewWithExecutor("muxiterm", executor)
	effect := &workflow.SideEffect{}

	err := adapter.Run(context.Background(), &workflow.RuntimeState{
		Workflow:  &reinv1.Workflow{Id: "wf-1"},
		Issue:     &reinv1.Issue{Id: "RN-21"},
		Execution: &reinv1.Execution{Id: "exec-rn-21-001"},
	}, workflow.Phase{
		ID:        "mux-step",
		Operation: "tmux detect",
		Inputs: map[string]string{
			"argv":             `["workspace-a"]`,
			"backend":          "tmux",
			"debug":            "true",
			"cwd":              "/repo/worktree",
			"env":              `{"MUXITERM_BACKEND":"tmux"}`,
			workflow.InputBail: "true",
		},
	}, workflow.DirectionForward, effect)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got, want := executor.command.Path, "muxiterm"; got != want {
		t.Fatalf("command.Path = %q, want %q", got, want)
	}
	if got, want := executor.command.Args, []string{"tmux", "detect", "workspace-a", "--backend", "tmux", "--debug"}; !slices.Equal(got, want) {
		t.Fatalf("command.Args = %v, want %v", got, want)
	}
	if got, want := executor.command.Dir, "/repo/worktree"; got != want {
		t.Fatalf("command.Dir = %q, want %q", got, want)
	}
	if !slices.Contains(executor.command.Env, "MUXITERM_BACKEND=tmux") || !slices.Contains(executor.command.Env, "REIN_EXECUTION_ID=exec-rn-21-001") {
		t.Fatalf("command.Env = %v, want muxiterm and rein context", executor.command.Env)
	}
	if got := effect.Outputs["session"]; got != "dev" {
		t.Fatalf("effect.Outputs[session] = %q, want %q", got, "dev")
	}
	if got := effect.Outputs["focused"]; got != "true" {
		t.Fatalf("effect.Outputs[focused] = %q, want %q", got, "true")
	}
	if got := effect.Outputs["panes"]; got != `["left","right"]` {
		t.Fatalf("effect.Outputs[panes] = %q, want %q", got, `["left","right"]`)
	}
	if got := effect.Outputs["stderr"]; got != "debug trace" {
		t.Fatalf("effect.Outputs[stderr] = %q, want %q", got, "debug trace")
	}
}

func TestRunUsesBackwardOperation(t *testing.T) {
	t.Parallel()

	executor := &fakeExecutor{result: Result{Stdout: `{"ok":true}`}}
	adapter := NewWithExecutor("muxiterm", executor)

	err := adapter.Run(context.Background(), nil, workflow.Phase{
		ID:                "mux-step",
		Operation:         "tmux detect",
		BackwardOperation: "registry release",
	}, workflow.DirectionBackward, &workflow.SideEffect{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := executor.command.Args, []string{"registry", "release"}; !slices.Equal(got, want) {
		t.Fatalf("command.Args = %v, want %v", got, want)
	}
}

func TestRunReturnsMuxitermEnvelopeFailures(t *testing.T) {
	t.Parallel()

	adapter := NewWithExecutor("muxiterm", &fakeExecutor{
		result: Result{
			Stdout: `{"ok":false,"error":{"code":"tmux-error","message":"tmux missing","hint":"install tmux"}}`,
		},
		err: errors.New("exit status 2"),
	})

	err := adapter.Run(context.Background(), nil, workflow.Phase{
		ID:        "mux-step",
		Operation: "tmux detect",
	}, workflow.DirectionForward, &workflow.SideEffect{})
	want := "muxiterm tmux detect: tmux-error: tmux missing (install tmux)"
	if err == nil || err.Error() != want {
		t.Fatalf("Run() error = %v, want %q", err, want)
	}
}

func TestRunRejectsMissingOperation(t *testing.T) {
	t.Parallel()

	err := NewWithExecutor("muxiterm", &fakeExecutor{}).Run(context.Background(), nil, workflow.Phase{
		ID: "mux-step",
	}, workflow.DirectionForward, &workflow.SideEffect{})
	if err == nil || err.Error() != "muxiterm operation is required" {
		t.Fatalf("Run() error = %v, want %q", err, "muxiterm operation is required")
	}
}

type fakeExecutor struct {
	command Command
	result  Result
	err     error
}

func (f *fakeExecutor) Run(_ context.Context, command Command) (Result, error) {
	f.command = command
	return f.result, f.err
}
