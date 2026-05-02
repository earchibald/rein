package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/earchibald/rein/adaptertest"
	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/workflow"
)

func TestGitHubTrackerAdapterConformance(t *testing.T) {
	t.Parallel()

	adapter := newGitHubTrackerManagedAdapter("/repo", nil)
	adaptertest.RunTracker(t, adaptertest.Spec{
		Descriptor:           adapter.Descriptor(),
		Implementation:       adapter,
		Contract:             (*ManagedAdapter)(nil),
		RequiredCapabilities: []string{"issue.sync", "branch.prepare", "worktree.create", "pull_request", "pull_request_review.poll", "merge"},
	})
}

func TestGitHubTrackerAdapterPrepareOpenReviewAndMerge(t *testing.T) {
	t.Parallel()

	var (
		mu               sync.Mutex
		requests         []string
		authHeaders      []string
		pullRequestTitle string
		pullRequestHead  string
		pullRequestBase  string
	)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		mu.Unlock()

		writeJSON := func(value any) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(value); err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
		}

		switch r.Method + " " + r.URL.Path {
		case "GET /repos/earchibald/rein/issues/22":
			writeJSON(map[string]any{
				"number":   22,
				"html_url": server.URL + "/earchibald/rein/issues/22",
				"title":    "GitHub tracker adapter",
				"body":     "Implement the first-party tracker adapter.",
				"state":    "open",
			})
		case "POST /repos/earchibald/rein/pulls":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			pullRequestTitle, _ = body["title"].(string)
			pullRequestHead, _ = body["head"].(string)
			pullRequestBase, _ = body["base"].(string)
			writeJSON(map[string]any{
				"number":   101,
				"html_url": server.URL + "/earchibald/rein/pull/101",
				"state":    "open",
			})
		case "GET /repos/earchibald/rein/pulls/101":
			writeJSON(map[string]any{
				"number":   101,
				"html_url": server.URL + "/earchibald/rein/pull/101",
				"state":    "open",
				"merged":   false,
			})
		case "GET /repos/earchibald/rein/pulls/101/reviews":
			writeJSON([]map[string]any{
				{"state": "COMMENTED", "user": map[string]any{"login": "octocat"}},
				{"state": "APPROVED", "user": map[string]any{"login": "hubot"}},
			})
		case "PUT /repos/earchibald/rein/pulls/101/merge":
			writeJSON(map[string]any{
				"sha":     "merge-rn-22-001",
				"merged":  true,
				"message": "Pull Request successfully merged",
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	root := filepath.Join(t.TempDir(), "rein")
	runner := &recordingRunner{branchExists: false}
	adapter := &githubTrackerAdapter{
		descriptor:         defaultGitHubTrackerDescriptor(),
		root:               root,
		httpClient:         server.Client(),
		lookupEnv:          func(name string) (string, bool) { return "super-secret-token", name == "RN22_GITHUB_TOKEN" },
		credentialRegistry: nil,
		commandRunner:      runner,
	}

	state := &workflow.RuntimeState{
		Workflow: &reinv1.Workflow{Id: "managed-issue-pr-review-merge"},
		Issue: &reinv1.Issue{
			Id:     "RN-22",
			Title:  "GitHub tracker adapter",
			Labels: map[string]string{},
		},
		Execution: &reinv1.Execution{
			Id: "exec-rn-22-001",
			Metadata: map[string]string{
				"github.repository":           "earchibald/rein",
				"github.api_base_url":         server.URL,
				"github.web_base_url":         server.URL,
				"base_branch":                 "main",
				"credential_ref.github_token": "env://RN22_GITHUB_TOKEN",
			},
		},
	}

	ctx := context.Background()
	prepareEffect := &workflow.SideEffect{}
	if err := adapter.Run(ctx, state, workflow.Phase{ID: "prepare-branch", Operation: "prepare"}, workflow.DirectionForward, prepareEffect); err != nil {
		t.Fatalf("Run(prepare) error = %v", err)
	}

	wantBranch := "issues/rn-22-github-tracker-adapter"
	wantWorktree := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-worktrees", "rn-22")
	if got := state.Execution.Metadata["issue_url"]; got != server.URL+"/earchibald/rein/issues/22" {
		t.Fatalf("issue_url = %q, want %q", got, server.URL+"/earchibald/rein/issues/22")
	}
	if got := state.Execution.Metadata["branch"]; got != wantBranch {
		t.Fatalf("branch = %q, want %q", got, wantBranch)
	}
	if got := state.Execution.Metadata["worktree"]; got != wantWorktree {
		t.Fatalf("worktree = %q, want %q", got, wantWorktree)
	}
	if got := state.Issue.GetDaemonState().GetBranch(); got != wantBranch {
		t.Fatalf("daemon_state.branch = %q, want %q", got, wantBranch)
	}
	if got := state.Issue.GetDaemonState().GetWorktree(); got != wantWorktree {
		t.Fatalf("daemon_state.worktree = %q, want %q", got, wantWorktree)
	}
	if got := state.Execution.Metadata["auth_mode"]; got != "credential" {
		t.Fatalf("auth_mode = %q, want credential", got)
	}
	if !reflect.DeepEqual(prepareEffect.Outputs, map[string]string{
		"issue_url": server.URL + "/earchibald/rein/issues/22",
		"branch":    wantBranch,
		"worktree":  wantWorktree,
	}) {
		t.Fatalf("prepare outputs = %+v", prepareEffect.Outputs)
	}

	openPREffect := &workflow.SideEffect{}
	if err := adapter.Run(ctx, state, workflow.Phase{ID: "open-pr", Operation: "open-pr"}, workflow.DirectionForward, openPREffect); err != nil {
		t.Fatalf("Run(open-pr) error = %v", err)
	}
	if pullRequestTitle != "GitHub tracker adapter" || pullRequestHead != wantBranch || pullRequestBase != "main" {
		t.Fatalf("pull request payload = title %q head %q base %q", pullRequestTitle, pullRequestHead, pullRequestBase)
	}
	if got := state.Execution.Metadata["pr_url"]; got != server.URL+"/earchibald/rein/pull/101" {
		t.Fatalf("pr_url = %q, want %q", got, server.URL+"/earchibald/rein/pull/101")
	}
	if got := state.Issue.GetDaemonState().GetPrUrl(); got != server.URL+"/earchibald/rein/pull/101" {
		t.Fatalf("daemon_state.pr_url = %q, want %q", got, server.URL+"/earchibald/rein/pull/101")
	}

	reviewEffect := &workflow.SideEffect{}
	if err := adapter.Run(ctx, state, workflow.Phase{ID: "poll-review", Operation: "poll-review"}, workflow.DirectionForward, reviewEffect); err != nil {
		t.Fatalf("Run(poll-review) error = %v", err)
	}
	if got := state.Execution.Metadata["review_state"]; got != "APPROVED" {
		t.Fatalf("review_state = %q, want APPROVED", got)
	}
	if got := state.Issue.GetDaemonState().GetReviewState(); got != "APPROVED" {
		t.Fatalf("daemon_state.review_state = %q, want APPROVED", got)
	}
	if got := state.Execution.Metadata["reviewed_by"]; got != "hubot" {
		t.Fatalf("reviewed_by = %q, want hubot", got)
	}

	mergeEffect := &workflow.SideEffect{}
	if err := adapter.Run(ctx, state, workflow.Phase{ID: "merge-pr", Operation: "merge"}, workflow.DirectionForward, mergeEffect); err != nil {
		t.Fatalf("Run(merge) error = %v", err)
	}
	if got := state.Execution.Metadata["merge_commit"]; got != "merge-rn-22-001" {
		t.Fatalf("merge_commit = %q, want merge-rn-22-001", got)
	}
	if got := state.Issue.GetDaemonState().GetMergeCommit(); got != "merge-rn-22-001" {
		t.Fatalf("daemon_state.merge_commit = %q, want merge-rn-22-001", got)
	}
	if got := state.Execution.Metadata["result"]; got != "merged" {
		t.Fatalf("result = %q, want merged", got)
	}
	if got := state.Execution.Metadata["pr_state"]; got != "MERGED" {
		t.Fatalf("pr_state = %q, want MERGED", got)
	}

	mu.Lock()
	defer mu.Unlock()
	wantRequests := []string{
		"GET /repos/earchibald/rein/issues/22",
		"POST /repos/earchibald/rein/pulls",
		"GET /repos/earchibald/rein/pulls/101",
		"GET /repos/earchibald/rein/pulls/101/reviews",
		"PUT /repos/earchibald/rein/pulls/101/merge",
	}
	if !slices.Equal(requests, wantRequests) {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
	for _, header := range authHeaders {
		if header != "Bearer super-secret-token" {
			t.Fatalf("Authorization header = %q, want bearer token", header)
		}
	}

	wantGit := []string{
		"git -C " + root + " show-ref --verify --quiet refs/heads/" + wantBranch,
		"git -C " + root + " worktree add -b " + wantBranch + " " + wantWorktree + " main",
	}
	if !slices.Equal(runner.calls, wantGit) {
		t.Fatalf("git calls = %v, want %v", runner.calls, wantGit)
	}
}

func TestGitHubTrackerAdapterAggregateReviewState(t *testing.T) {
	t.Parallel()

	state, reviewers := aggregateReviewState([]githubReview{
		{State: "APPROVED", User: struct {
			Login string `json:"login"`
		}{Login: "hubot"}},
		{State: "COMMENTED", User: struct {
			Login string `json:"login"`
		}{Login: "octocat"}},
		{State: "CHANGES_REQUESTED", User: struct {
			Login string `json:"login"`
		}{Login: "dependabot"}},
		{State: "APPROVED", User: struct {
			Login string `json:"login"`
		}{Login: "dependabot"}},
	})
	if state != "APPROVED" {
		t.Fatalf("aggregateReviewState() state = %q, want APPROVED", state)
	}
	if !slices.Equal(reviewers, []string{"dependabot", "hubot"}) {
		t.Fatalf("aggregateReviewState() reviewers = %v, want [dependabot hubot]", reviewers)
	}
}

func TestGitHubTrackerAdapterPollReviewClearsReviewedByWhenPending(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/earchibald/rein/pulls/101":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":   101,
				"html_url": server.URL + "/earchibald/rein/pull/101",
				"state":    "open",
				"merged":   false,
			})
		case "GET /repos/earchibald/rein/pulls/101/reviews":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"state": "COMMENTED", "user": map[string]any{"login": "octocat"}},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	adapter := &githubTrackerAdapter{
		descriptor:    defaultGitHubTrackerDescriptor(),
		root:          filepath.Join(t.TempDir(), "rein"),
		httpClient:    server.Client(),
		commandRunner: &recordingRunner{},
	}
	state := &workflow.RuntimeState{
		Workflow: &reinv1.Workflow{Id: "wf"},
		Issue:    &reinv1.Issue{Id: "RN-22", ProjectId: "project-rein", Labels: map[string]string{}},
		Execution: &reinv1.Execution{
			Id: "exec-rn-22-001",
			Metadata: map[string]string{
				"github.repository":   "earchibald/rein",
				"github.api_base_url": server.URL,
				"github.web_base_url": server.URL,
				"github_token":        "token",
				"pr_url":              server.URL + "/earchibald/rein/pull/101",
				"reviewed_by":         "hubot",
			},
		},
	}

	if err := adapter.Run(context.Background(), state, workflow.Phase{Operation: "poll-review"}, workflow.DirectionForward, &workflow.SideEffect{}); err != nil {
		t.Fatalf("Run(poll-review) error = %v", err)
	}
	if got := state.Execution.Metadata["review_state"]; got != "PENDING" {
		t.Fatalf("review_state = %q, want PENDING", got)
	}
	if got := state.Issue.GetDaemonState().GetReviewState(); got != "PENDING" {
		t.Fatalf("daemon_state.review_state = %q, want PENDING", got)
	}
	if _, ok := state.Execution.Metadata["reviewed_by"]; ok {
		t.Fatalf("reviewed_by = %q, want cleared", state.Execution.Metadata["reviewed_by"])
	}
}

func TestGitHubTrackerAdapterPrepareRejectsExistingNonWorktreePath(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.URL.Path != "/repos/earchibald/rein/issues/22" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":   22,
			"html_url": server.URL + "/earchibald/rein/issues/22",
			"title":    "GitHub tracker adapter",
			"state":    "open",
		})
	}))
	defer server.Close()

	root := filepath.Join(t.TempDir(), "rein")
	worktreePath := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-worktrees", "rn-22")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", worktreePath, err)
	}

	adapter := &githubTrackerAdapter{
		descriptor:    defaultGitHubTrackerDescriptor(),
		root:          root,
		httpClient:    server.Client(),
		commandRunner: &recordingRunner{},
	}
	state := &workflow.RuntimeState{
		Workflow: &reinv1.Workflow{Id: "wf"},
		Issue: &reinv1.Issue{
			Id:        "RN-22",
			ProjectId: "project-rein",
			Title:     "GitHub tracker adapter",
			Labels:    map[string]string{},
		},
		Execution: &reinv1.Execution{
			Id: "exec-rn-22-001",
			Metadata: map[string]string{
				"github.repository":   "earchibald/rein",
				"github.api_base_url": server.URL,
				"github.web_base_url": server.URL,
				"github_token":        "token",
			},
		},
	}

	err := adapter.Run(context.Background(), state, workflow.Phase{Operation: "prepare"}, workflow.DirectionForward, &workflow.SideEffect{})
	if err == nil || !strings.Contains(err.Error(), "is not a git worktree") {
		t.Fatalf("Run(prepare) error = %v, want non-worktree error", err)
	}
}

func TestRepositoryFromRemoteURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "https", raw: "https://github.com/earchibald/rein.git", want: "earchibald/rein"},
		{name: "ssh scp", raw: "git@github.com:earchibald/rein.git", want: "earchibald/rein"},
		{name: "ssh url", raw: "ssh://git@github.com/earchibald/rein.git", want: "earchibald/rein"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := repositoryFromRemoteURL(tt.raw)
			if err != nil {
				t.Fatalf("repositoryFromRemoteURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("repositoryFromRemoteURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

type recordingRunner struct {
	calls        []string
	branchExists bool
}

func (r *recordingRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	command := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, command)
	if strings.Contains(command, "show-ref") {
		if r.branchExists {
			return nil, nil
		}
		return nil, &fakeExitError{}
	}
	return []byte(""), nil
}

type fakeExitError struct{}

func (*fakeExitError) Error() string   { return "exit status 1" }
func (*fakeExitError) ExitCode() int   { return 1 }
func (*fakeExitError) Stderr() []byte  { return nil }
func (*fakeExitError) String() string  { return "exit status 1" }
func (*fakeExitError) Unwrap() error   { return nil }
func (*fakeExitError) Timeout() bool   { return false }
func (*fakeExitError) Temporary() bool { return false }

func TestGitHubTrackerAdapterCleanupBranch(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "rein")
	runner := &cleanupRunner{}
	adapter := &githubTrackerAdapter{
		descriptor:         defaultGitHubTrackerDescriptor(),
		root:               root,
		httpClient:         http.DefaultClient,
		lookupEnv:          nil,
		credentialRegistry: nil,
		commandRunner:      runner,
	}
	state := &workflow.RuntimeState{
		Workflow:  &reinv1.Workflow{Id: "wf"},
		Issue:     &reinv1.Issue{Id: "RN-22", ProjectId: "project-rein", Labels: map[string]string{}},
		Execution: &reinv1.Execution{Id: "exec-rn-22-001", Metadata: map[string]string{"github.repository": "earchibald/rein", "github_token": "token", "worktree": filepath.Join(filepath.Dir(root), filepath.Base(root)+"-worktrees", "rn-22")}},
	}
	effect := &workflow.SideEffect{}
	if err := adapter.Run(context.Background(), state, workflow.Phase{BackwardOperation: "cleanup-branch"}, workflow.DirectionBackward, effect); err != nil {
		t.Fatalf("Run(cleanup-branch) error = %v", err)
	}
	if got := state.Execution.Metadata["branch_cleanup"]; got != "true" {
		t.Fatalf("branch_cleanup = %q, want true", got)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("cleanup calls = %v, want 1", runner.calls)
	}
}

type cleanupRunner struct{ calls []string }

func (r *cleanupRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	return nil, nil
}
