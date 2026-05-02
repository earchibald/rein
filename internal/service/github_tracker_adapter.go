package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/credentials"
	"github.com/earchibald/rein/internal/workflow"
)

const githubTrackerAdapterID = "tracker-github"

var (
	managedAdapterFactories = map[string]func(string, *reinv1.Adapter) ManagedAdapter{
		githubTrackerAdapterID: newGitHubTrackerManagedAdapter,
	}

	githubIssueNumberPattern = regexp.MustCompile(`(\d+)$`)
)

type githubTrackerAdapter struct {
	descriptor         *reinv1.Adapter
	root               string
	httpClient         *http.Client
	credentialRegistry *credentials.Registry
	lookupEnv          func(string) (string, bool)
	commandRunner      commandRunner
}

type commandRunner interface {
	CombinedOutput(context.Context, string, ...string) ([]byte, error)
}

type exitCoder interface {
	ExitCode() int
}

type execCommandRunner struct{}

type githubTrackerConfig struct {
	repository  string
	owner       string
	repo        string
	token       string
	authMode    string
	apiBaseURL  string
	webBaseURL  string
	issueNumber int
}

type githubIssue struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
}

type githubPullRequest struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
}

type githubReview struct {
	State string `json:"state"`
	User  struct {
		Login string `json:"login"`
	} `json:"user"`
}

type githubMergeResult struct {
	SHA     string `json:"sha"`
	Merged  bool   `json:"merged"`
	Message string `json:"message"`
}

func (execCommandRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func newGitHubTrackerManagedAdapter(root string, descriptor *reinv1.Adapter) ManagedAdapter {
	if descriptor == nil {
		descriptor = defaultGitHubTrackerDescriptor()
	}
	return &githubTrackerAdapter{
		descriptor:         proto.Clone(descriptor).(*reinv1.Adapter),
		root:               root,
		httpClient:         http.DefaultClient,
		credentialRegistry: credentials.NewBuiltinRegistry(credentials.BuiltinOptions{}),
		lookupEnv:          os.LookupEnv,
		commandRunner:      execCommandRunner{},
	}
}

func defaultGitHubTrackerDescriptor() *reinv1.Adapter {
	return &reinv1.Adapter{
		Id:          githubTrackerAdapterID,
		Name:        "GitHub Tracker",
		Category:    reinv1.AdapterCategory_ADAPTER_CATEGORY_TRACKER,
		Description: "First-party GitHub issue and pull-request tracker adapter",
		Version:     "0.1.0",
		Enabled:     true,
		Capabilities: map[string]string{
			"issue.sync":               "true",
			"branch.prepare":           "true",
			"worktree.create":          "true",
			"pull_request":             "true",
			"pull_request_review.poll": "true",
			"merge":                    "true",
		},
	}
}

func (a *githubTrackerAdapter) Descriptor() *reinv1.Adapter {
	if a == nil || a.descriptor == nil {
		return nil
	}
	return proto.Clone(a.descriptor).(*reinv1.Adapter)
}

func (a *githubTrackerAdapter) Run(ctx context.Context, state *workflow.RuntimeState, phase workflow.Phase, direction workflow.Direction, effect *workflow.SideEffect) error {
	if state == nil || state.Issue == nil || state.Execution == nil {
		return errors.New("github tracker adapter requires issue and execution state")
	}
	if state.Execution.Metadata == nil {
		state.Execution.Metadata = map[string]string{}
	}
	normalizeIssue(state.Issue)

	config, err := a.resolveConfig(ctx, state)
	if err != nil {
		return err
	}
	state.Execution.Metadata["auth_mode"] = config.authMode
	state.Execution.Metadata["github.repository"] = config.repository

	switch direction {
	case workflow.DirectionForward:
		return a.runForward(ctx, config, state, phase, effect)
	case workflow.DirectionBackward:
		return a.runBackward(ctx, config, state, phase, effect)
	default:
		return fmt.Errorf("unsupported workflow direction %q", direction)
	}
}

func (a *githubTrackerAdapter) runForward(ctx context.Context, config githubTrackerConfig, state *workflow.RuntimeState, phase workflow.Phase, effect *workflow.SideEffect) error {
	switch strings.TrimSpace(phase.Operation) {
	case "prepare":
		return a.prepareIssue(ctx, config, state, effect)
	case "open-pr", "pull-request", "pull_request":
		return a.openPullRequest(ctx, config, state, effect)
	case "poll-review", "review", "review-poll":
		return a.pollPullRequestReview(ctx, config, state, effect)
	case "merge":
		return a.mergePullRequest(ctx, config, state, effect)
	default:
		return fmt.Errorf("unsupported GitHub tracker operation %q", phase.Operation)
	}
}

func (a *githubTrackerAdapter) runBackward(ctx context.Context, config githubTrackerConfig, state *workflow.RuntimeState, phase workflow.Phase, effect *workflow.SideEffect) error {
	switch strings.TrimSpace(phase.BackwardOperation) {
	case "":
		return nil
	case "cleanup-branch":
		return a.cleanupBranch(ctx, state, effect)
	case "close-pr":
		return a.closePullRequest(ctx, config, state, effect)
	case "dismiss-review":
		state.Execution.Metadata["review_state"] = "DISMISSED"
		ensureIssueDaemonState(state.Issue).ReviewState = "DISMISSED"
		setEffectOutput(effect, "review_state", "DISMISSED")
		return nil
	case "reopen-merge":
		state.Execution.Metadata["result"] = "reopened"
		setEffectOutput(effect, "result", "reopened")
		return nil
	default:
		return fmt.Errorf("unsupported GitHub tracker backward operation %q", phase.BackwardOperation)
	}
}

func (a *githubTrackerAdapter) prepareIssue(ctx context.Context, config githubTrackerConfig, state *workflow.RuntimeState, effect *workflow.SideEffect) error {
	var issue githubIssue
	if err := a.doJSON(ctx, http.MethodGet, config, path.Join("repos", config.owner, config.repo, "issues", strconv.Itoa(config.issueNumber)), nil, &issue); err != nil {
		return err
	}

	title := strings.TrimSpace(state.Issue.GetTitle())
	if title == "" {
		title = strings.TrimSpace(issue.Title)
		state.Issue.Title = title
	}
	if strings.TrimSpace(state.Issue.GetSummary()) == "" {
		state.Issue.Summary = strings.TrimSpace(issue.Body)
	}

	branch := strings.TrimSpace(state.Execution.Metadata["branch"])
	if branch == "" {
		branch = issueBranchName(state.Issue.GetId(), title)
	}
	worktreePath := strings.TrimSpace(state.Execution.Metadata["worktree"])
	if worktreePath == "" {
		worktreePath = a.defaultWorktreePath(state.Issue.GetId())
	}
	if err := a.ensureWorktree(ctx, branch, worktreePath, state.Execution.Metadata["base_branch"]); err != nil {
		return err
	}

	state.Execution.Metadata["issue_url"] = issue.HTMLURL
	state.Execution.Metadata["branch"] = branch
	state.Execution.Metadata["worktree"] = worktreePath
	issueState := ensureIssueDaemonState(state.Issue)
	issueState.Branch = branch
	issueState.Worktree = worktreePath
	setEffectOutput(effect, "issue_url", issue.HTMLURL)
	setEffectOutput(effect, "branch", branch)
	setEffectOutput(effect, "worktree", worktreePath)
	return nil
}

func (a *githubTrackerAdapter) openPullRequest(ctx context.Context, config githubTrackerConfig, state *workflow.RuntimeState, effect *workflow.SideEffect) error {
	branch := strings.TrimSpace(state.Execution.Metadata["branch"])
	if branch == "" {
		return errors.New("branch metadata is required before opening a pull request")
	}
	baseBranch := strings.TrimSpace(state.Execution.Metadata["base_branch"])
	if baseBranch == "" {
		baseBranch = "main"
	}

	request := map[string]any{
		"title": state.Issue.GetTitle(),
		"head":  branch,
		"base":  baseBranch,
		"body":  pullRequestBody(state, config.issueNumber),
	}
	var pullRequest githubPullRequest
	if err := a.doJSON(ctx, http.MethodPost, config, path.Join("repos", config.owner, config.repo, "pulls"), request, &pullRequest); err != nil {
		return err
	}

	state.Execution.Metadata["pr_url"] = pullRequest.HTMLURL
	state.Execution.Metadata["pr_state"] = normalizePullRequestState(pullRequest)
	state.Execution.Metadata["github.pull_number"] = strconv.Itoa(pullRequest.Number)
	ensureIssueDaemonState(state.Issue).PrUrl = pullRequest.HTMLURL
	setEffectOutput(effect, "pr_url", pullRequest.HTMLURL)
	setEffectOutput(effect, "pr_state", state.Execution.Metadata["pr_state"])
	return nil
}

func (a *githubTrackerAdapter) pollPullRequestReview(ctx context.Context, config githubTrackerConfig, state *workflow.RuntimeState, effect *workflow.SideEffect) error {
	pullNumber, err := resolvePullRequestNumber(state)
	if err != nil {
		return err
	}

	var pullRequest githubPullRequest
	if err := a.doJSON(ctx, http.MethodGet, config, path.Join("repos", config.owner, config.repo, "pulls", strconv.Itoa(pullNumber)), nil, &pullRequest); err != nil {
		return err
	}
	state.Execution.Metadata["pr_url"] = firstNonEmpty(pullRequest.HTMLURL, state.Execution.Metadata["pr_url"])
	state.Execution.Metadata["pr_state"] = normalizePullRequestState(pullRequest)
	state.Execution.Metadata["github.pull_number"] = strconv.Itoa(pullNumber)

	var reviews []githubReview
	if err := a.doJSON(ctx, http.MethodGet, config, path.Join("repos", config.owner, config.repo, "pulls", strconv.Itoa(pullNumber), "reviews"), nil, &reviews); err != nil {
		return err
	}

	reviewState, reviewers := aggregateReviewState(reviews)
	state.Execution.Metadata["review_state"] = reviewState
	if len(reviewers) > 0 {
		state.Execution.Metadata["reviewed_by"] = strings.Join(reviewers, ",")
		setEffectOutput(effect, "reviewed_by", state.Execution.Metadata["reviewed_by"])
	} else {
		delete(state.Execution.Metadata, "reviewed_by")
	}
	ensureIssueDaemonState(state.Issue).ReviewState = reviewState
	setEffectOutput(effect, "pr_state", state.Execution.Metadata["pr_state"])
	setEffectOutput(effect, "review_state", reviewState)
	return nil
}

func (a *githubTrackerAdapter) mergePullRequest(ctx context.Context, config githubTrackerConfig, state *workflow.RuntimeState, effect *workflow.SideEffect) error {
	if state.Execution.Metadata["review_state"] != "APPROVED" {
		return errors.New("review approval is required before merge")
	}
	pullNumber, err := resolvePullRequestNumber(state)
	if err != nil {
		return err
	}
	baseBranch := strings.TrimSpace(state.Execution.Metadata["base_branch"])
	if baseBranch == "" {
		baseBranch = "main"
	}

	request := map[string]string{"merge_method": "merge"}
	var result githubMergeResult
	if err := a.doJSON(ctx, http.MethodPut, config, path.Join("repos", config.owner, config.repo, "pulls", strconv.Itoa(pullNumber), "merge"), request, &result); err != nil {
		return err
	}
	if !result.Merged || strings.TrimSpace(result.SHA) == "" {
		return fmt.Errorf("github merge did not complete: %s", strings.TrimSpace(result.Message))
	}

	state.Execution.Metadata["merge_commit"] = result.SHA
	state.Execution.Metadata["integration_branch"] = baseBranch
	state.Execution.Metadata["result"] = "merged"
	state.Execution.Metadata["pr_state"] = "MERGED"
	ensureIssueDaemonState(state.Issue).MergeCommit = result.SHA
	setEffectOutput(effect, "merge_commit", result.SHA)
	setEffectOutput(effect, "integration_branch", baseBranch)
	setEffectOutput(effect, "result", "merged")
	return nil
}

func (a *githubTrackerAdapter) cleanupBranch(ctx context.Context, state *workflow.RuntimeState, effect *workflow.SideEffect) error {
	worktreePath := strings.TrimSpace(state.Execution.Metadata["worktree"])
	if worktreePath == "" {
		return nil
	}
	if strings.TrimSpace(a.root) == "" {
		return errors.New("git root is required to clean up worktrees")
	}
	if _, err := a.commandRunner.CombinedOutput(ctx, "git", "-C", a.root, "worktree", "remove", "--force", worktreePath); err != nil {
		return fmt.Errorf("remove worktree %q: %w", worktreePath, err)
	}
	state.Execution.Metadata["branch_cleanup"] = "true"
	setEffectOutput(effect, "branch_cleanup", "true")
	return nil
}

func (a *githubTrackerAdapter) closePullRequest(ctx context.Context, config githubTrackerConfig, state *workflow.RuntimeState, effect *workflow.SideEffect) error {
	pullNumber, err := resolvePullRequestNumber(state)
	if err != nil {
		return err
	}
	var pullRequest githubPullRequest
	if err := a.doJSON(ctx, http.MethodPatch, config, path.Join("repos", config.owner, config.repo, "pulls", strconv.Itoa(pullNumber)), map[string]string{"state": "closed"}, &pullRequest); err != nil {
		return err
	}
	state.Execution.Metadata["pr_state"] = normalizePullRequestState(pullRequest)
	setEffectOutput(effect, "pr_state", state.Execution.Metadata["pr_state"])
	return nil
}

func (a *githubTrackerAdapter) ensureWorktree(ctx context.Context, branch, worktreePath, baseBranch string) error {
	if strings.TrimSpace(a.root) == "" {
		return errors.New("git root is required to prepare a worktree")
	}
	if baseBranch == "" {
		baseBranch = "main"
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return fmt.Errorf("create worktree parent for %q: %w", worktreePath, err)
	}
	if _, err := os.Stat(worktreePath); err == nil {
		if gitWorktreePathExists(worktreePath) {
			return nil
		}
		return fmt.Errorf("worktree path %q already exists but is not a git worktree", worktreePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect worktree %q: %w", worktreePath, err)
	}

	if branchExists, err := a.branchExists(ctx, branch); err != nil {
		return err
	} else if branchExists {
		if _, err := a.commandRunner.CombinedOutput(ctx, "git", "-C", a.root, "worktree", "add", worktreePath, branch); err != nil {
			return fmt.Errorf("create worktree %q for branch %q: %w", worktreePath, branch, err)
		}
		return nil
	}

	if _, err := a.commandRunner.CombinedOutput(ctx, "git", "-C", a.root, "worktree", "add", "-b", branch, worktreePath, baseBranch); err != nil {
		return fmt.Errorf("create worktree %q for new branch %q: %w", worktreePath, branch, err)
	}
	return nil
}

func (a *githubTrackerAdapter) branchExists(ctx context.Context, branch string) (bool, error) {
	output, err := a.commandRunner.CombinedOutput(ctx, "git", "-C", a.root, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	if exitError, ok := err.(exitCoder); ok && exitError.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("check git branch %q: %w%s", branch, err, commandOutputSuffix(output))
}

func (a *githubTrackerAdapter) resolveConfig(ctx context.Context, state *workflow.RuntimeState) (githubTrackerConfig, error) {
	token, authMode, err := a.resolveToken(ctx, state)
	if err != nil {
		return githubTrackerConfig{}, err
	}

	repository := firstNonEmpty(
		state.Execution.Metadata["github.repository"],
		state.Execution.Metadata["github_repository"],
		state.Execution.Metadata["repository"],
		state.Issue.Labels["github.repository"],
	)
	if repository == "" {
		repository, err = a.repositoryFromGitRemote(ctx)
		if err != nil {
			return githubTrackerConfig{}, err
		}
	}
	owner, repo, err := splitRepository(repository)
	if err != nil {
		return githubTrackerConfig{}, err
	}

	issueNumber, err := resolveIssueNumber(state)
	if err != nil {
		return githubTrackerConfig{}, err
	}

	apiBaseURL := firstNonEmpty(state.Execution.Metadata["github.api_base_url"], state.Execution.Metadata["github_api_base_url"])
	if apiBaseURL == "" {
		apiBaseURL = "https://api.github.com"
	}
	webBaseURL := firstNonEmpty(state.Execution.Metadata["github.web_base_url"], state.Execution.Metadata["github_web_base_url"])
	if webBaseURL == "" {
		webBaseURL = deriveGitHubWebBaseURL(apiBaseURL)
	}

	return githubTrackerConfig{
		repository:  repository,
		owner:       owner,
		repo:        repo,
		token:       token,
		authMode:    authMode,
		apiBaseURL:  strings.TrimRight(apiBaseURL, "/"),
		webBaseURL:  strings.TrimRight(webBaseURL, "/"),
		issueNumber: issueNumber,
	}, nil
}

func (a *githubTrackerAdapter) resolveToken(ctx context.Context, state *workflow.RuntimeState) (token, authMode string, err error) {
	if reference := strings.TrimSpace(state.Execution.Metadata["credential_ref.github_token"]); reference != "" {
		registry := a.credentialRegistry
		if registry == nil {
			registry = credentials.NewBuiltinRegistry(credentials.BuiltinOptions{})
		}
		value, err := registry.Resolve(ctx, reference, credentials.ExecutionScope{
			ProjectID:   state.Issue.GetProjectId(),
			WorkflowID:  state.Workflow.GetId(),
			ExecutionID: state.Execution.GetId(),
			LookupEnv:   a.lookupEnv,
		})
		if err != nil {
			return "", "", err
		}
		return value, "credential", nil
	}
	if token := strings.TrimSpace(firstNonEmpty(state.Execution.Metadata["github_token"], state.Execution.Metadata["github.token"])); token != "" {
		return token, "token", nil
	}
	return "", "", errors.New("github token is required via credential_ref.github_token or github_token")
}

func (a *githubTrackerAdapter) repositoryFromGitRemote(ctx context.Context) (string, error) {
	if strings.TrimSpace(a.root) == "" {
		return "", errors.New("github.repository is required when git root is unavailable")
	}
	output, err := a.commandRunner.CombinedOutput(ctx, "git", "-C", a.root, "config", "--get", "remote.origin.url")
	if err != nil {
		return "", fmt.Errorf("resolve repository from git remote: %w%s", err, commandOutputSuffix(output))
	}
	repository, parseErr := repositoryFromRemoteURL(strings.TrimSpace(string(output)))
	if parseErr != nil {
		return "", parseErr
	}
	return repository, nil
}

func (a *githubTrackerAdapter) defaultWorktreePath(issueID string) string {
	root := strings.TrimSpace(a.root)
	if root == "" {
		return filepath.Join("worktrees", strings.ToLower(issueID))
	}
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+"-worktrees", strings.ToLower(issueID))
}

func (a *githubTrackerAdapter) doJSON(ctx context.Context, method string, config githubTrackerConfig, route string, requestBody, responseBody any) error {
	body, err := encodeJSON(requestBody)
	if err != nil {
		return err
	}
	endpoint, err := url.JoinPath(config.apiBaseURL, route)
	if err != nil {
		return fmt.Errorf("build GitHub API URL for %q: %w", route, err)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create GitHub %s %q request: %w", method, route, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+config.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := a.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub %s %q request: %w", method, route, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&failure)
		message := strings.TrimSpace(failure.Message)
		if message == "" {
			message = resp.Status
		}
		return fmt.Errorf("GitHub %s %q: %s", method, route, message)
	}
	if responseBody == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(responseBody); err != nil {
		return fmt.Errorf("decode GitHub %s %q response: %w", method, route, err)
	}
	return nil
}

func pullRequestBody(state *workflow.RuntimeState, issueNumber int) string {
	summary := strings.TrimSpace(state.Issue.GetSummary())
	if summary == "" {
		return fmt.Sprintf("Closes #%d", issueNumber)
	}
	return summary + "\n\nCloses #" + strconv.Itoa(issueNumber)
}

func issueBranchName(issueID, title string) string {
	slug := slugify(title)
	if slug == "" {
		return "issues/" + strings.ToLower(strings.TrimSpace(issueID))
	}
	return "issues/" + strings.ToLower(strings.TrimSpace(issueID)) + "-" + slug
}

func slugify(value string) string {
	var builder strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastHyphen = false
		case !lastHyphen:
			builder.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func splitRepository(repository string) (owner, repo string, err error) {
	segments := strings.Split(strings.Trim(repository, "/"), "/")
	if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
		return "", "", fmt.Errorf("github.repository %q must use owner/repo format", repository)
	}
	return segments[0], segments[1], nil
}

func resolveIssueNumber(state *workflow.RuntimeState) (int, error) {
	for _, raw := range []string{
		state.Execution.Metadata["github.issue_number"],
		state.Execution.Metadata["github_issue_number"],
		state.Issue.Labels["github.issue_number"],
	} {
		if number, ok := parseOptionalPositiveInt(raw); ok {
			return number, nil
		}
	}
	match := githubIssueNumberPattern.FindStringSubmatch(strings.TrimSpace(state.Issue.GetId()))
	if len(match) != 2 {
		return 0, fmt.Errorf("cannot infer GitHub issue number from issue id %q", state.Issue.GetId())
	}
	number, _ := strconv.Atoi(match[1])
	return number, nil
}

func resolvePullRequestNumber(state *workflow.RuntimeState) (int, error) {
	for _, raw := range []string{
		state.Execution.Metadata["github.pull_number"],
		state.Execution.Metadata["github_pull_number"],
	} {
		if number, ok := parseOptionalPositiveInt(raw); ok {
			return number, nil
		}
	}
	prURL := strings.TrimSpace(state.Execution.Metadata["pr_url"])
	if prURL == "" {
		return 0, errors.New("pull request number is required before polling or merging")
	}
	parsed, err := url.Parse(prURL)
	if err != nil {
		return 0, fmt.Errorf("parse pull request URL %q: %w", prURL, err)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) == 0 {
		return 0, fmt.Errorf("parse pull request URL %q: missing path segments", prURL)
	}
	number, ok := parseOptionalPositiveInt(segments[len(segments)-1])
	if !ok {
		return 0, fmt.Errorf("parse pull request URL %q: missing numeric pull request id", prURL)
	}
	return number, nil
}

func parseOptionalPositiveInt(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	number, err := strconv.Atoi(raw)
	if err != nil || number <= 0 {
		return 0, false
	}
	return number, true
}

func normalizePullRequestState(pullRequest githubPullRequest) string {
	if pullRequest.Merged {
		return "MERGED"
	}
	state := strings.ToUpper(strings.TrimSpace(pullRequest.State))
	if state == "" {
		return "OPEN"
	}
	return state
}

func aggregateReviewState(reviews []githubReview) (state string, reviewers []string) {
	latestByReviewer := map[string]string{}
	for _, review := range reviews {
		login := strings.TrimSpace(review.User.Login)
		reviewState := strings.ToUpper(strings.TrimSpace(review.State))
		if login == "" || reviewState == "" {
			continue
		}
		switch reviewState {
		case "COMMENTED", "PENDING":
			continue
		default:
			latestByReviewer[login] = reviewState
		}
	}

	var changesRequested []string
	var approved []string
	for reviewer, reviewState := range latestByReviewer {
		switch reviewState {
		case "CHANGES_REQUESTED":
			changesRequested = append(changesRequested, reviewer)
		case "APPROVED":
			approved = append(approved, reviewer)
		}
	}
	sort.Strings(changesRequested)
	sort.Strings(approved)
	if len(changesRequested) > 0 {
		return "CHANGES_REQUESTED", changesRequested
	}
	if len(approved) > 0 {
		return "APPROVED", approved
	}
	return "PENDING", nil
}

func repositoryFromRemoteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("git remote origin URL is empty")
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("parse git remote URL %q: %w", raw, err)
		}
		return repositoryFromPath(parsed.Path)
	}
	if prefix, rest, ok := strings.Cut(raw, ":"); ok && strings.Contains(prefix, "@") {
		return repositoryFromPath(rest)
	}
	return repositoryFromPath(raw)
}

func repositoryFromPath(raw string) (string, error) {
	trimmed := strings.Trim(strings.TrimSuffix(strings.TrimSpace(raw), ".git"), "/")
	segments := strings.Split(trimmed, "/")
	if len(segments) < 2 {
		return "", fmt.Errorf("cannot infer owner/repo from git remote %q", raw)
	}
	return strings.Join(segments[len(segments)-2:], "/"), nil
}

func deriveGitHubWebBaseURL(apiBaseURL string) string {
	apiBaseURL = strings.TrimRight(strings.TrimSpace(apiBaseURL), "/")
	switch {
	case apiBaseURL == "https://api.github.com":
		return "https://github.com"
	case strings.HasSuffix(apiBaseURL, "/api/v3"):
		return strings.TrimSuffix(apiBaseURL, "/api/v3")
	case strings.HasSuffix(apiBaseURL, "/api"):
		return strings.TrimSuffix(apiBaseURL, "/api")
	default:
		return apiBaseURL
	}
}

func gitWorktreePathExists(worktreePath string) bool {
	_, err := os.Stat(filepath.Join(worktreePath, ".git"))
	return err == nil
}

func encodeJSON(value any) (*bytes.Reader, error) {
	if value == nil {
		return bytes.NewReader(nil), nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode GitHub request: %w", err)
	}
	return bytes.NewReader(payload), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func setEffectOutput(effect *workflow.SideEffect, key, value string) {
	if effect == nil || strings.TrimSpace(value) == "" {
		return
	}
	if effect.Outputs == nil {
		effect.Outputs = map[string]string{}
	}
	effect.Outputs[key] = value
}

func commandOutputSuffix(output []byte) string {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return ""
	}
	return ": " + trimmed
}
