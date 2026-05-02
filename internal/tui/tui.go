package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/grpc"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
)

const defaultRefreshInterval = 5 * time.Second

type Options struct {
	InstanceName    string
	Network         string
	Address         string
	RefreshInterval time.Duration
}

type dataLoader interface {
	loadBase(context.Context) (baseData, error)
	loadDetail(context.Context, string) (*reinv1.InspectExecutionResponse, error)
}

type grpcLoader struct {
	projects   reinv1.ProjectServiceClient
	issues     reinv1.IssueServiceClient
	executions reinv1.ExecutionServiceClient
	workflows  reinv1.WorkflowServiceClient
	adapters   reinv1.AdapterServiceClient
}

type baseData struct {
	projects    []*reinv1.Project
	issues      []*reinv1.Issue
	executions  []*reinv1.Execution
	workflows   map[string]*reinv1.Workflow
	adapters    map[string]*reinv1.Adapter
	refreshedAt time.Time
}

type baseLoadedMsg struct {
	data baseData
	err  error
}

type detailLoadedMsg struct {
	id     string
	detail *reinv1.InspectExecutionResponse
	err    error
}

type refreshTickMsg time.Time

type focusArea int

const (
	focusProjects focusArea = iota
	focusIssues
	focusExecutions
)

type model struct {
	loader dataLoader
	opts   Options

	width  int
	height int

	focus         focusArea
	loadingBase   bool
	loadingDetail bool
	showDrilldown bool

	base      baseData
	baseErr   string
	detail    *reinv1.InspectExecutionResponse
	detailErr string

	selectedProjectID   string
	selectedIssueID     string
	selectedExecutionID string
}

func Run(conn grpc.ClientConnInterface, opts Options) error {
	program := tea.NewProgram(newModel(newGRPCLoader(conn), opts), tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func newGRPCLoader(conn grpc.ClientConnInterface) *grpcLoader {
	return &grpcLoader{
		projects:   reinv1.NewProjectServiceClient(conn),
		issues:     reinv1.NewIssueServiceClient(conn),
		executions: reinv1.NewExecutionServiceClient(conn),
		workflows:  reinv1.NewWorkflowServiceClient(conn),
		adapters:   reinv1.NewAdapterServiceClient(conn),
	}
}

func newModel(loader dataLoader, opts Options) model {
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = defaultRefreshInterval
	}
	return model{
		loader:        loader,
		opts:          opts,
		focus:         focusProjects,
		loadingBase:   true,
		showDrilldown: true,
	}
}

func (l *grpcLoader) loadBase(ctx context.Context) (baseData, error) {
	projectsResp, err := l.projects.ListProjects(ctx, &reinv1.ListProjectsRequest{})
	if err != nil {
		return baseData{}, err
	}
	issuesResp, err := l.issues.ListIssues(ctx, &reinv1.ListIssuesRequest{})
	if err != nil {
		return baseData{}, err
	}
	executionsResp, err := l.executions.ListExecutions(ctx, &reinv1.ListExecutionsRequest{})
	if err != nil {
		return baseData{}, err
	}
	workflowsResp, err := l.workflows.ListWorkflows(ctx, &reinv1.ListWorkflowsRequest{})
	if err != nil {
		return baseData{}, err
	}
	adaptersResp, err := l.adapters.ListAdapters(ctx, &reinv1.ListAdaptersRequest{})
	if err != nil {
		return baseData{}, err
	}

	workflows := make(map[string]*reinv1.Workflow, len(workflowsResp.GetWorkflows()))
	for _, workflow := range workflowsResp.GetWorkflows() {
		if workflow == nil {
			continue
		}
		workflows[workflow.GetId()] = workflow
	}
	adapters := make(map[string]*reinv1.Adapter, len(adaptersResp.GetAdapters()))
	for _, adapter := range adaptersResp.GetAdapters() {
		if adapter == nil {
			continue
		}
		adapters[adapter.GetId()] = adapter
	}

	return baseData{
		projects:    projectsResp.GetProjects(),
		issues:      issuesResp.GetIssues(),
		executions:  executionsResp.GetExecutions(),
		workflows:   workflows,
		adapters:    adapters,
		refreshedAt: time.Now(),
	}, nil
}

func (l *grpcLoader) loadDetail(ctx context.Context, executionID string) (*reinv1.InspectExecutionResponse, error) {
	if strings.TrimSpace(executionID) == "" {
		return nil, nil
	}
	return l.executions.InspectExecution(ctx, &reinv1.InspectExecutionRequest{Id: executionID})
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadBaseCmd(), m.refreshCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case baseLoadedMsg:
		m.loadingBase = false
		if msg.err != nil {
			m.baseErr = msg.err.Error()
			return m, nil
		}
		m.baseErr = ""
		m.base = msg.data
		previousExecutionID := m.selectedExecutionID
		m.syncSelection()
		if m.selectedExecutionID != previousExecutionID {
			m.detail = nil
			m.detailErr = ""
		}
		if m.selectedExecutionID != "" {
			m.loadingDetail = true
			return m, m.loadDetailCmd(m.selectedExecutionID)
		}
		m.loadingDetail = false
		m.detail = nil
		m.detailErr = ""
		return m, nil
	case detailLoadedMsg:
		if msg.id != m.selectedExecutionID {
			return m, nil
		}
		m.loadingDetail = false
		if msg.err != nil {
			m.detail = nil
			m.detailErr = msg.err.Error()
			return m, nil
		}
		m.detail = msg.detail
		m.detailErr = ""
		return m, nil
	case refreshTickMsg:
		m.loadingBase = true
		return m, tea.Batch(m.loadBaseCmd(), m.refreshCmd())
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			m.focus = (m.focus + 1) % 3
			return m, nil
		case "shift+tab":
			m.focus = (m.focus + 2) % 3
			return m, nil
		case "left", "h":
			m.focus = maxFocus(m.focus-1, focusProjects)
			return m, nil
		case "right", "l":
			m.focus = minFocus(m.focus+1, focusExecutions)
			return m, nil
		case "enter":
			m.showDrilldown = !m.showDrilldown
			return m, nil
		case "r":
			m.loadingBase = true
			return m, m.loadBaseCmd()
		case "up", "k":
			return m.moveSelection(-1)
		case "down", "j":
			return m.moveSelection(1)
		}
	}
	return m, nil
}

func (m model) View() string {
	width := m.width
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 36
	}

	header := headerStyle.Render(fmt.Sprintf("rein tui • instance %s • %s %s", fallback(m.opts.InstanceName, "live"), fallback(m.opts.Network, "unix"), fallback(m.opts.Address, "default")))
	summary := summaryStyle.Render(m.summaryLine())
	footer := footerStyle.Render("tab focus • ↑↓ move • enter compact/expanded • r refresh • q quit")
	bodyHeight := maxInt(12, height-4)
	leftWidth := minInt(44, maxInt(32, width/3))
	rightWidth := maxInt(40, width-leftWidth-1)

	left := m.renderNavigator(leftWidth, bodyHeight)
	right := m.renderDetail(rightWidth, bodyHeight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	return strings.Join([]string{header, summary, body, footer}, "\n")
}

func (m model) loadBaseCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		data, err := m.loader.loadBase(ctx)
		return baseLoadedMsg{data: data, err: err}
	}
}

func (m model) loadDetailCmd(executionID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		detail, err := m.loader.loadDetail(ctx, executionID)
		return detailLoadedMsg{id: executionID, detail: detail, err: err}
	}
}

func (m model) refreshCmd() tea.Cmd {
	return tea.Tick(m.opts.RefreshInterval, func(t time.Time) tea.Msg { return refreshTickMsg(t) })
}

func (m model) moveSelection(delta int) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.focus {
	case focusProjects:
		m.selectedProjectID = moveID(m.selectedProjectID, projectIDs(m.base.projects), delta)
		m.selectedIssueID = ""
		m.selectedExecutionID = ""
		m.detail = nil
		m.detailErr = ""
		m.syncSelection()
		if m.selectedExecutionID != "" {
			m.loadingDetail = true
			cmd = m.loadDetailCmd(m.selectedExecutionID)
		}
	case focusIssues:
		m.selectedIssueID = moveID(m.selectedIssueID, issueIDs(m.filteredIssues()), delta)
		m.selectedExecutionID = ""
		m.detail = nil
		m.detailErr = ""
		m.syncSelection()
		if m.selectedExecutionID != "" {
			m.loadingDetail = true
			cmd = m.loadDetailCmd(m.selectedExecutionID)
		}
	case focusExecutions:
		ids := executionIDs(m.filteredExecutions())
		previous := m.selectedExecutionID
		m.selectedExecutionID = moveID(m.selectedExecutionID, ids, delta)
		if previous != m.selectedExecutionID {
			m.detail = nil
			m.detailErr = ""
			if m.selectedExecutionID != "" {
				m.loadingDetail = true
				cmd = m.loadDetailCmd(m.selectedExecutionID)
			} else {
				m.loadingDetail = false
			}
		}
	}
	return m, cmd
}

func (m *model) syncSelection() {
	m.selectedProjectID = ensureID(m.selectedProjectID, projectIDs(m.base.projects))
	m.selectedIssueID = ensureID(m.selectedIssueID, issueIDs(m.filteredIssues()))
	m.selectedExecutionID = ensureID(m.selectedExecutionID, executionIDs(m.filteredExecutions()))
}

func (m model) filteredIssues() []*reinv1.Issue {
	if m.selectedProjectID == "" {
		return m.base.issues
	}
	filtered := make([]*reinv1.Issue, 0, len(m.base.issues))
	for _, issue := range m.base.issues {
		if issue != nil && issue.GetProjectId() == m.selectedProjectID {
			filtered = append(filtered, issue)
		}
	}
	return filtered
}

func (m model) filteredExecutions() []*reinv1.Execution {
	if m.selectedIssueID == "" {
		return m.base.executions
	}
	filtered := make([]*reinv1.Execution, 0, len(m.base.executions))
	for _, execution := range m.base.executions {
		if execution != nil && execution.GetIssueId() == m.selectedIssueID {
			filtered = append(filtered, execution)
		}
	}
	return filtered
}

func (m model) selectedProject() *reinv1.Project {
	for _, project := range m.base.projects {
		if project != nil && project.GetId() == m.selectedProjectID {
			return project
		}
	}
	return nil
}

func (m model) selectedIssue() *reinv1.Issue {
	for _, issue := range m.base.issues {
		if issue != nil && issue.GetId() == m.selectedIssueID {
			return issue
		}
	}
	return nil
}

func (m model) selectedExecution() *reinv1.Execution {
	for _, execution := range m.base.executions {
		if execution != nil && execution.GetId() == m.selectedExecutionID {
			return execution
		}
	}
	return nil
}

func (m model) selectedWorkflow() *reinv1.Workflow {
	if m.detail != nil && m.detail.GetWorkflow() != nil {
		return m.detail.GetWorkflow()
	}
	issue := m.selectedIssue()
	if issue == nil {
		return nil
	}
	return m.base.workflows[issue.GetWorkflowId()]
}

func (m model) renderNavigator(width, height int) string {
	available := maxInt(9, height-2)
	projectHeight := maxInt(4, available/4)
	issueHeight := maxInt(5, available/3)
	executionHeight := maxInt(5, available-projectHeight-issueHeight)

	projects := renderPanel("Projects", renderSelectableList(projectItems(m.base.projects), m.selectedProjectID, projectHeight-2), width, projectHeight, m.focus == focusProjects)
	issues := renderPanel("Issues", renderSelectableList(issueItems(m.filteredIssues()), m.selectedIssueID, issueHeight-2), width, issueHeight, m.focus == focusIssues)
	executions := renderPanel("Executions", renderSelectableList(executionItems(m.filteredExecutions()), m.selectedExecutionID, executionHeight-2), width, executionHeight, m.focus == focusExecutions)
	return lipgloss.JoinVertical(lipgloss.Left, projects, issues, executions)
}

func (m model) renderDetail(width, height int) string {
	if m.baseErr != "" && len(m.base.projects) == 0 && len(m.base.issues) == 0 && len(m.base.executions) == 0 {
		return renderPanel("Overview", "Failed to load daemon state:\n\n"+m.baseErr, width, height, false)
	}

	lines := []string{
		fmt.Sprintf("Projects: %d • Issues: %d • Executions: %d • Workflows: %d", len(m.base.projects), len(m.base.issues), len(m.base.executions), len(m.base.workflows)),
	}
	if refreshed := m.base.refreshedAt; !refreshed.IsZero() {
		lines = append(lines, "Last refresh: "+refreshed.Format(time.RFC822))
	}

	if project := m.selectedProject(); project != nil {
		lines = append(lines, "", "Project", fmt.Sprintf("  %s (%s)", project.GetDisplayName(), statusText(project.GetStatus().String())), fmt.Sprintf("  %s", fallback(project.GetSummary(), "No summary")))
	}
	if issue := m.selectedIssue(); issue != nil {
		lines = append(lines, "", "Issue", fmt.Sprintf("  %s", issue.GetTitle()), fmt.Sprintf("  %s • %s • assignee %s", statusText(issue.GetStatus().String()), priorityText(issue.GetPriority().String()), fallback(issue.GetAssignee(), "unassigned")))
		if integration := issue.GetLabels()["integration_status"]; integration != "" {
			lines = append(lines, fmt.Sprintf("  workflow %s • integration %s", fallback(issue.GetWorkflowId(), "none"), integration))
		} else {
			lines = append(lines, fmt.Sprintf("  workflow %s", fallback(issue.GetWorkflowId(), "none")))
		}
	}
	if workflow := m.selectedWorkflow(); workflow != nil {
		lines = append(lines, "", fmt.Sprintf("Workflow • %s %s", workflow.GetName(), fallback(workflow.GetVersion(), "")))
		for _, line := range m.workflowStatusLines(workflow) {
			lines = append(lines, "  "+line)
		}
	}
	if execution := m.selectedExecution(); execution != nil {
		lines = append(lines, "", "Execution", fmt.Sprintf("  %s • %s", execution.GetId(), statusText(execution.GetStatus().String())), fmt.Sprintf("  requested by %s", fallback(execution.GetRequestedBy(), "unknown")))
		if m.loadingDetail {
			lines = append(lines, "", "Drilldown loading…")
		} else if m.detailErr != "" {
			lines = append(lines, "", "Drilldown unavailable", "  "+m.detailErr)
		} else if m.detail != nil {
			lines = append(lines, m.executionDetailLines()...)
		}
	}

	return renderPanel("Overview", strings.Join(lines, "\n"), width, height, false)
}

func (m model) workflowStatusLines(workflow *reinv1.Workflow) []string {
	if workflow == nil {
		return []string{"No workflow selected."}
	}
	statuses := map[string]*reinv1.ExecutionTaskStep{}
	if m.detail != nil {
		for _, step := range m.detail.GetTaskSteps() {
			if step == nil {
				continue
			}
			current, ok := statuses[step.GetPhaseId()]
			if !ok || step.GetSequence() >= current.GetSequence() {
				statuses[step.GetPhaseId()] = step
			}
		}
	}
	lines := make([]string, 0, len(workflow.GetSteps()))
	for _, step := range workflow.GetSteps() {
		if step == nil {
			continue
		}
		state := statuses[step.GetId()]
		icon := "·"
		statusLabel := "pending"
		if state != nil {
			icon = statusIcon(state.GetStatus())
			statusLabel = state.GetStatus()
		}
		lane := step.GetInputs()["lane"]
		if lane == "" {
			lane = "trunk"
		}
		lines = append(lines, fmt.Sprintf("%s %s [%s] %s", icon, step.GetName(), lane, strings.ToLower(statusLabel)))
	}
	if len(lines) == 0 {
		return []string{"Workflow has no steps."}
	}
	return lines
}

func (m model) executionDetailLines() []string {
	if m.detail == nil {
		return nil
	}
	lines := []string{}
	if m.showDrilldown {
		lines = append(lines, "", "Task steps")
		for _, step := range m.detail.GetTaskSteps() {
			if step == nil {
				continue
			}
			lines = append(lines, fmt.Sprintf("  %s #%d %s (%s via %s)", statusIcon(step.GetStatus()), step.GetSequence(), fallback(step.GetPhaseName(), step.GetPhaseId()), step.GetOperation(), fallback(step.GetAdapterId(), "unknown")))
		}
		lines = append(lines, "", "Side effects")
		for _, effect := range m.detail.GetSideEffects() {
			if effect == nil {
				continue
			}
			detail := effectSummary(effect)
			lines = append(lines, fmt.Sprintf("  %s #%d %s", statusIcon(effect.GetStatus()), effect.GetSequence(), detail))
		}
	}
	metadata := sortedMapLines(m.detail.GetExecution().GetMetadata())
	if len(metadata) > 0 {
		lines = append(lines, "", "Metadata")
		for _, line := range metadata {
			lines = append(lines, "  "+line)
		}
	}
	lines = append(lines, "", "Looking glass")
	lookingGlass := m.detail.GetLookingGlass()
	if lookingGlass == nil {
		lines = append(lines, "  No looking-glass status available.")
	} else {
		state := "disabled"
		if lookingGlass.GetSupported() && lookingGlass.GetAvailable() {
			state = "available"
		} else if lookingGlass.GetSupported() {
			state = "gated"
		}
		lines = append(lines, fmt.Sprintf("  %s", state))
		if len(lookingGlass.GetAdapterIds()) > 0 {
			lines = append(lines, fmt.Sprintf("  adapters: %s", strings.Join(lookingGlass.GetAdapterIds(), ", ")))
		}
		if lookingGlass.GetStatus() != "" {
			lines = append(lines, "  "+lookingGlass.GetStatus())
		}
	}
	return lines
}

func (m model) summaryLine() string {
	parts := []string{}
	if m.loadingBase {
		parts = append(parts, "refreshing")
	}
	if m.baseErr != "" {
		parts = append(parts, "last error: "+m.baseErr)
	}
	if len(parts) == 0 {
		parts = append(parts, "daemon data loaded")
	}
	return strings.Join(parts, " • ")
}

type selectableItem struct {
	id    string
	label string
	meta  string
	state string
}

func projectItems(projects []*reinv1.Project) []selectableItem {
	items := make([]selectableItem, 0, len(projects))
	for _, project := range projects {
		if project == nil {
			continue
		}
		items = append(items, selectableItem{id: project.GetId(), label: fallback(project.GetDisplayName(), project.GetId()), meta: fallback(project.GetSlug(), "-"), state: statusText(project.GetStatus().String())})
	}
	return items
}

func issueItems(issues []*reinv1.Issue) []selectableItem {
	items := make([]selectableItem, 0, len(issues))
	for _, issue := range issues {
		if issue == nil {
			continue
		}
		items = append(items, selectableItem{id: issue.GetId(), label: issue.GetId() + " • " + issue.GetTitle(), meta: fallback(issue.GetWorkflowId(), "no workflow"), state: statusText(issue.GetStatus().String())})
	}
	return items
}

func executionItems(executions []*reinv1.Execution) []selectableItem {
	items := make([]selectableItem, 0, len(executions))
	for _, execution := range executions {
		if execution == nil {
			continue
		}
		items = append(items, selectableItem{id: execution.GetId(), label: execution.GetId(), meta: fallback(execution.GetAdapterId(), execution.GetWorkflowId()), state: statusText(execution.GetStatus().String())})
	}
	return items
}

func renderSelectableList(items []selectableItem, selectedID string, height int) string {
	if len(items) == 0 {
		return "No items."
	}
	selectedIndex := indexForID(selectedID, selectableIDs(items))
	start, end := visibleRange(len(items), selectedIndex, maxInt(1, height))
	lines := make([]string, 0, end-start)
	for _, item := range items[start:end] {
		prefix := "  "
		line := fmt.Sprintf("%s%s", item.label, metaStyle.Render(" • "+item.state))
		if item.meta != "" {
			line += metaStyle.Render(" • " + item.meta)
		}
		if item.id == selectedID {
			prefix = accentStyle.Render("› ")
			line = selectedStyle.Render(line)
		}
		lines = append(lines, prefix+line)
	}
	return strings.Join(lines, "\n")
}

func renderPanel(title, body string, width, height int, focused bool) string {
	style := panelStyle.Copy().Width(width).Height(height)
	if focused {
		style = focusedPanelStyle.Copy().Width(width).Height(height)
	}
	return style.Render(titleStyle.Render(title) + "\n" + clipLines(body, maxInt(1, height-2), width-4))
}

func effectSummary(effect *reinv1.ExecutionSideEffect) string {
	parts := []string{fallback(effect.GetPhaseName(), effect.GetPhaseId()), strings.ToLower(effect.GetStatus()), effect.GetOperation()}
	if outputs := sortedMapLines(effect.GetOutputs()); len(outputs) > 0 {
		parts = append(parts, outputs[0])
	}
	if effect.GetError() != "" {
		parts = append(parts, effect.GetError())
	}
	return strings.Join(parts, " • ")
}

func sortedMapLines(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", key, values[key]))
	}
	return lines
}

func statusIcon(status string) string {
	switch strings.ToLower(status) {
	case "succeeded", "applied":
		return "✓"
	case "failed":
		return "✕"
	case "running", "pending":
		return "•"
	case "canceled":
		return "◌"
	default:
		return "·"
	}
}

func statusText(value string) string {
	return strings.ToLower(strings.TrimPrefix(value, prefixForEnum(value)))
}

func priorityText(value string) string {
	return strings.ToLower(strings.TrimPrefix(value, prefixForEnum(value)))
}

func prefixForEnum(value string) string {
	if i := strings.Index(value, "_"); i >= 0 {
		prefix := value[:i+1]
		if j := strings.LastIndex(value[:len(value)-1], "_"); j >= 0 {
			return value[:j+1]
		}
		return prefix
	}
	return ""
}

func fallback(value, alt string) string {
	if strings.TrimSpace(value) == "" {
		return alt
	}
	return value
}

func clipLines(body string, height, width int) string {
	lines := strings.Split(body, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		if lipgloss.Width(line) > width && width > 1 {
			lines[i] = lipgloss.NewStyle().MaxWidth(width).Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

func projectIDs(projects []*reinv1.Project) []string {
	ids := make([]string, 0, len(projects))
	for _, project := range projects {
		if project != nil {
			ids = append(ids, project.GetId())
		}
	}
	return ids
}

func issueIDs(issues []*reinv1.Issue) []string {
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue != nil {
			ids = append(ids, issue.GetId())
		}
	}
	return ids
}

func executionIDs(executions []*reinv1.Execution) []string {
	ids := make([]string, 0, len(executions))
	for _, execution := range executions {
		if execution != nil {
			ids = append(ids, execution.GetId())
		}
	}
	return ids
}

func selectableIDs(items []selectableItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.id)
	}
	return ids
}

func ensureID(current string, ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	for _, id := range ids {
		if id == current {
			return current
		}
	}
	return ids[0]
}

func moveID(current string, ids []string, delta int) string {
	if len(ids) == 0 {
		return ""
	}
	index := indexForID(current, ids)
	index += delta
	if index < 0 {
		index = 0
	}
	if index >= len(ids) {
		index = len(ids) - 1
	}
	return ids[index]
}

func indexForID(id string, ids []string) int {
	for i, candidate := range ids {
		if candidate == id {
			return i
		}
	}
	return 0
}

func visibleRange(total, selected, height int) (int, int) {
	if total <= height {
		return 0, total
	}
	start := selected - height/2
	if start < 0 {
		start = 0
	}
	end := start + height
	if end > total {
		end = total
		start = end - height
	}
	return start, end
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minFocus(a, b focusArea) focusArea {
	if a < b {
		return a
	}
	return b
}

func maxFocus(a, b focusArea) focusArea {
	if a > b {
		return a
	}
	return b
}

var (
	headerStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	summaryStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	footerStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	panelStyle        = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1)
	focusedPanelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("86")).Padding(0, 1)
	titleStyle        = lipgloss.NewStyle().Bold(true)
	selectedStyle     = lipgloss.NewStyle().Bold(true)
	accentStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	metaStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)
