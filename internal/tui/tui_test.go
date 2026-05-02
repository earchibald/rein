package tui

import (
	"strings"
	"testing"

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
