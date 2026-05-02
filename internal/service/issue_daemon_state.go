package service

import (
	"strings"

	"google.golang.org/protobuf/proto"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
)

var daemonManagedIssueLabelKeys = []string{
	"branch",
	"worktree",
	"pr_url",
	"review_state",
	"merge_commit",
	"integration_status",
}

func cloneIssueDaemonState(state *reinv1.IssueDaemonState) *reinv1.IssueDaemonState {
	if state == nil {
		return nil
	}
	return proto.Clone(state).(*reinv1.IssueDaemonState)
}

func normalizeIssue(issue *reinv1.Issue) {
	if issue == nil {
		return
	}
	if issue.Labels == nil {
		issue.Labels = map[string]string{}
	}
	if issue.DaemonState == nil {
		issue.DaemonState = daemonStateFromLegacyLabels(issue.Labels)
	}
	for _, key := range daemonManagedIssueLabelKeys {
		delete(issue.Labels, key)
	}
}

func ensureIssueDaemonState(issue *reinv1.Issue) *reinv1.IssueDaemonState {
	normalizeIssue(issue)
	if issue.DaemonState == nil {
		issue.DaemonState = &reinv1.IssueDaemonState{}
	}
	return issue.DaemonState
}

func resetIssueDaemonState(issue *reinv1.Issue, executionID string) *reinv1.IssueDaemonState {
	state := ensureIssueDaemonState(issue)
	state.ExecutionId = strings.TrimSpace(executionID)
	state.Branch = ""
	state.Worktree = ""
	state.PrUrl = ""
	state.ReviewState = ""
	state.MergeCommit = ""
	state.IntegrationStatus = "running"
	return state
}

func daemonStateFromLegacyLabels(labels map[string]string) *reinv1.IssueDaemonState {
	if len(labels) == 0 {
		return nil
	}
	state := &reinv1.IssueDaemonState{
		Branch:            strings.TrimSpace(labels["branch"]),
		Worktree:          strings.TrimSpace(labels["worktree"]),
		PrUrl:             strings.TrimSpace(labels["pr_url"]),
		ReviewState:       strings.TrimSpace(labels["review_state"]),
		MergeCommit:       strings.TrimSpace(labels["merge_commit"]),
		IntegrationStatus: strings.TrimSpace(labels["integration_status"]),
	}
	if state.Branch == "" && state.Worktree == "" && state.PrUrl == "" && state.ReviewState == "" && state.MergeCommit == "" && state.IntegrationStatus == "" {
		return nil
	}
	return state
}

func mergeIssueForPersistence(latest, stateful *reinv1.Issue) *reinv1.Issue {
	if stateful == nil {
		return nil
	}
	if latest == nil {
		merged := proto.Clone(stateful).(*reinv1.Issue)
		normalizeIssue(merged)
		return merged
	}

	merged := proto.Clone(latest).(*reinv1.Issue)
	normalizeIssue(merged)
	normalizeIssue(stateful)

	if strings.TrimSpace(merged.GetTitle()) == "" {
		merged.Title = stateful.GetTitle()
	}
	if strings.TrimSpace(merged.GetSummary()) == "" {
		merged.Summary = stateful.GetSummary()
	}
	if strings.TrimSpace(merged.GetWorkflowId()) == "" {
		merged.WorkflowId = stateful.GetWorkflowId()
	}
	if strings.TrimSpace(merged.GetAssignee()) == "" {
		merged.Assignee = stateful.GetAssignee()
	}
	if merged.GetPriority() == reinv1.IssuePriority_ISSUE_PRIORITY_UNSPECIFIED {
		merged.Priority = stateful.GetPriority()
	}
	merged.Status = stateful.GetStatus()
	merged.UpdatedTime = stateful.GetUpdatedTime()
	merged.DaemonState = cloneIssueDaemonState(stateful.GetDaemonState())
	return merged
}

func mergeExecutionForPersistence(latest, current *reinv1.Execution) *reinv1.Execution {
	if current == nil {
		return nil
	}
	if latest == nil {
		merged := proto.Clone(current).(*reinv1.Execution)
		if merged.Metadata == nil {
			merged.Metadata = map[string]string{}
		}
		return merged
	}

	merged := proto.Clone(latest).(*reinv1.Execution)
	if merged.Metadata == nil {
		merged.Metadata = map[string]string{}
	}
	for key, value := range current.GetMetadata() {
		merged.Metadata[key] = value
	}
	merged.AdapterId = current.GetAdapterId()
	merged.Status = current.GetStatus()
	merged.RequestedBy = current.GetRequestedBy()
	merged.CreatedTime = current.GetCreatedTime()
	merged.StartedTime = current.GetStartedTime()
	merged.FinishedTime = current.GetFinishedTime()
	return merged
}
