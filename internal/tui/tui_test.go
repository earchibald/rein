package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
)

func TestSyncSelectionFiltersIssueAndExecutionByProject(t *testing.T) {
	m := model{
		base: baseData{
			projects:   []*reinv1.Project{{Id: "p1"}, {Id: "p2"}},
			issues:     []*reinv1.Issue{{Id: "i1", ProjectId: "p1"}, {Id: "i2", ProjectId: "p2"}},
			executions: []*reinv1.Execution{{Id: "e1", IssueId: "i1"}, {Id: "e2", IssueId: "i2"}},
		},
		selectedProjectID: "p2",
	}

	m.syncSelection()

	if m.selectedProjectID != "p2" {
		t.Fatalf("selectedProjectID = %q, want p2", m.selectedProjectID)
	}
	if m.selectedIssueID != "i2" {
		t.Fatalf("selectedIssueID = %q, want i2", m.selectedIssueID)
	}
	if m.selectedExecutionID != "e2" {
		t.Fatalf("selectedExecutionID = %q, want e2", m.selectedExecutionID)
	}
}

func TestExecutionDetailLinesShowLookingGlassGate(t *testing.T) {
	m := model{
		showDrilldown: true,
		detail: &reinv1.InspectExecutionResponse{
			Execution:    &reinv1.Execution{Metadata: map[string]string{"result": "merged"}},
			TaskSteps:    []*reinv1.ExecutionTaskStep{{Sequence: 1, PhaseId: "prepare", PhaseName: "Prepare", Status: "succeeded", Operation: "prepare", AdapterId: "tracker"}},
			SideEffects:  []*reinv1.ExecutionSideEffect{{Sequence: 1, PhaseId: "prepare", PhaseName: "Prepare", Status: "applied", Operation: "prepare", Outputs: map[string]string{"branch": "issues/rn-18"}}},
			LookingGlass: &reinv1.LookingGlassState{Supported: true, Available: false, AdapterIds: []string{"review-bot"}, Status: "Adapters advertise tail support, but the daemon does not expose looking-glass streaming yet."},
		},
	}

	output := strings.Join(m.executionDetailLines(), "\n")
	for _, want := range []string{"Task steps", "Prepare", "branch=issues/rn-18", "Looking glass", "gated", "review-bot"} {
		if !strings.Contains(output, want) {
			t.Fatalf("executionDetailLines() missing %q\n%s", want, output)
		}
	}
}

func TestIssueIntegrationStatusPrefersDaemonState(t *testing.T) {
	issue := &reinv1.Issue{
		Labels: map[string]string{"integration_status": "stale"},
		DaemonState: &reinv1.IssueDaemonState{
			IntegrationStatus: "merged",
		},
	}

	if got := issueIntegrationStatus(issue); got != "merged" {
		t.Fatalf("issueIntegrationStatus() = %q, want merged", got)
	}
}

func TestRenderDetailShowsOverflowIndicatorsAndScrolls(t *testing.T) {
	t.Parallel()

	steps := make([]*reinv1.ExecutionTaskStep, 0, 16)
	for i := 0; i < 16; i++ {
		steps = append(steps, &reinv1.ExecutionTaskStep{
			Sequence:  int32(i + 1),
			PhaseId:   "prepare",
			PhaseName: "Prepare",
			Status:    "running",
			Operation: "prepare",
			AdapterId: "tracker",
		})
	}

	m := model{
		width:  120,
		height: 16,
		base: baseData{
			executions:  []*reinv1.Execution{{Id: "e1", Status: reinv1.ExecutionStatus_EXECUTION_STATUS_RUNNING, RequestedBy: "tester"}},
			refreshedAt: time.Unix(1700000000, 0),
		},
		selectedExecutionID: "e1",
		detail: &reinv1.InspectExecutionResponse{
			Execution:    &reinv1.Execution{Metadata: map[string]string{"result": "running"}},
			TaskSteps:    steps,
			LookingGlass: &reinv1.LookingGlassState{},
		},
	}

	initial := m.renderDetail(70, 12)
	if !strings.Contains(initial, "↓ ") {
		t.Fatalf("renderDetail() missing bottom overflow indicator\n%s", initial)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	scrolled, ok := updated.(model)
	if !ok {
		t.Fatalf("Update() returned %T, want model", updated)
	}
	if scrolled.detailScrollOffset == 0 {
		t.Fatalf("detailScrollOffset = %d, want > 0", scrolled.detailScrollOffset)
	}

	view := scrolled.renderDetail(70, 12)
	if !strings.Contains(view, "↑ ") {
		t.Fatalf("renderDetail() missing top overflow indicator after scroll\n%s", view)
	}
}

func TestBaseLoadedKeepsExistingDetailForSameExecution(t *testing.T) {
	t.Parallel()

	execution := &reinv1.Execution{Id: "e1", Status: reinv1.ExecutionStatus_EXECUTION_STATUS_RUNNING}
	m := model{
		base:                baseData{executions: []*reinv1.Execution{execution}},
		detail:              &reinv1.InspectExecutionResponse{Execution: execution},
		selectedExecutionID: "e1",
	}

	updated, cmd := m.Update(baseLoadedMsg{data: baseData{
		executions:  []*reinv1.Execution{execution},
		refreshedAt: time.Unix(1700000000, 0),
	}})
	if cmd != nil {
		t.Fatalf("Update() returned unexpected command for unchanged selection")
	}

	next, ok := updated.(model)
	if !ok {
		t.Fatalf("Update() returned %T, want model", updated)
	}
	if next.detail == nil {
		t.Fatalf("detail = nil, want existing drilldown to be preserved")
	}
	if next.selectedExecutionID != "e1" {
		t.Fatalf("selectedExecutionID = %q, want e1", next.selectedExecutionID)
	}
}
