// Package ui implements the Bubble Tea model for notion-tui: a left tree pane
// and a right content pane, with on-demand page fetch and editor handoff.
package ui

import (
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"

	"notion-tui/internal/cache"
	"notion-tui/internal/notion"
	"notion-tui/internal/tree"
)

type pane int

const (
	paneTree pane = iota
	paneContent
)

// flatRow is one visible line in the rendered tree (after collapsing/expanding).
type flatRow struct {
	node  *tree.Node
	depth int
}

// Load stages shown in the startup progress list, in display order.
const (
	stageCache = iota
	stagePages
	stageDBs
	stageBuild
	numStages
)

type stageInfo struct {
	label  string
	detail string
	done   bool
	active bool
}

type cachedPage struct {
	lastEdited string
	markdown   string
}

type layoutConfig struct {
	bodyHeight   int
	footerHeight int
	treeWidth    int
	contentWidth int
	singlePane   bool
}

type clearStatusMsg struct{}

// Model is the top-level Bubble Tea model.
type Model struct {
	width, height int
	focus         pane
	fullWidth     bool

	roots      []*tree.Node
	expanded   map[string]bool
	rows       []flatRow
	cursor     int
	treeOffset int
	contentRaw string

	spinner   spinner.Model
	loadingTr bool
	treeErr   error
	stages    [numStages]stageInfo
	loadCh    chan loadEvent

	viewport        viewport.Model
	loadingPage     bool
	pageErr         error
	databaseLoading bool
	databaseErr     error
	databaseID      string
	databaseTable   notion.DataSourceTable
	activeID        string
	activeTitle     string
	renderer        *glamour.TermRenderer
	pageCache       map[string]cachedPage

	filtering bool
	filter    string
	showHelp  bool
	message   string
}

func New() Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	vp := viewport.New(0, 0)
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(0),
	)
	m := Model{
		focus:     paneTree,
		expanded:  map[string]bool{},
		spinner:   sp,
		loadingTr: true,
		viewport:  vp,
		renderer:  renderer,
		pageCache: map[string]cachedPage{},
		loadCh:    make(chan loadEvent, 4),
		width:     80,
		height:    24,
	}
	m.stages[stageCache] = stageInfo{label: "Checking local cache"}
	m.stages[stagePages] = stageInfo{label: "Querying Notion for pages"}
	m.stages[stageDBs] = stageInfo{label: "Querying Notion for databases"}
	m.stages[stageBuild] = stageInfo{label: "Building page tree"}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, loadCachedTreeCmd, startLoadCmd(m.loadCh))
}

type cachedTreeMsg struct{ snap cache.Snapshot }
type loadEvent struct {
	stage  int
	detail string
	done   bool
	final  bool
	snap   cache.Snapshot
	err    error
}
type pageLoadedMsg struct {
	id         string
	title      string
	lastEdited string
	content    string
	err        error
}
type databaseLoadedMsg struct {
	id    string
	title string
	table notion.DataSourceTable
	err   error
}
type editorDoneMsg struct{ err error }

func loadCachedTreeCmd() tea.Msg {
	snap, err := cache.Load()
	if err != nil || len(snap.Pages) == 0 {
		return nil
	}
	return cachedTreeMsg{snap: snap}
}

func startLoadCmd(ch chan loadEvent) tea.Cmd {
	return func() tea.Msg {
		go runLoad(ch)
		return <-ch
	}
}

func waitForLoadEvent(ch chan loadEvent) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func runLoad(ch chan loadEvent) {
	ch <- loadEvent{stage: stagePages, detail: "starting..."}
	tPages := time.Now()
	pages, err := notion.SearchAllPages(func(n int) {
		ch <- loadEvent{stage: stagePages, detail: fmt.Sprintf("%d fetched...", n)}
	})
	if err != nil {
		ch <- loadEvent{stage: stagePages, err: err, final: true}
		return
	}
	ch <- loadEvent{stage: stagePages, done: true, detail: fmt.Sprintf("%d pages in %s", len(pages), time.Since(tPages).Round(time.Millisecond))}

	ch <- loadEvent{stage: stageDBs, detail: "starting..."}
	tDBs := time.Now()
	dbs, err := notion.SearchAllDataSources(func(n int) {
		ch <- loadEvent{stage: stageDBs, detail: fmt.Sprintf("%d fetched...", n)}
	})
	if err != nil {
		ch <- loadEvent{stage: stageDBs, err: err, final: true}
		return
	}
	ch <- loadEvent{stage: stageDBs, done: true, detail: fmt.Sprintf("%d databases in %s", len(dbs), time.Since(tDBs).Round(time.Millisecond))}

	ch <- loadEvent{stage: stageBuild, detail: "starting..."}
	tBuild := time.Now()
	snap := cache.Snapshot{Pages: pages, DataSources: dbs}
	_ = cache.Save(snap)
	ch <- loadEvent{stage: stageBuild, done: true, final: true, snap: snap, detail: time.Since(tBuild).Round(time.Millisecond).String()}
}

func fetchPageCmd(id, title, lastEdited string) tea.Cmd {
	return func() tea.Msg {
		md, err := notion.GetPageMarkdown(id)
		return pageLoadedMsg{id: id, title: title, lastEdited: lastEdited, content: md, err: err}
	}
}

func fetchDatabaseCmd(id, title string) tea.Cmd {
	return func() tea.Msg {
		table, err := notion.QueryDataSource(id)
		return databaseLoadedMsg{id: id, title: title, table: table, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case spinner.TickMsg:
		if m.loadingTr || m.loadingPage {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case cachedTreeMsg:
		m.stages[stageCache] = stageInfo{label: "Checking local cache", done: true, detail: fmt.Sprintf("%d pages from last run", len(msg.snap.Pages))}
		if m.roots == nil {
			m.setPages(msg.snap)
		}
		return m, nil
	case loadEvent:
		m.stages[msg.stage].detail = msg.detail
		m.stages[msg.stage].active = !msg.done
		m.stages[msg.stage].done = msg.done
		if !m.stages[stageCache].done {
			m.stages[stageCache] = stageInfo{label: "Checking local cache", done: true, detail: "none found"}
		}
		if msg.err != nil {
			m.loadingTr = false
			m.treeErr = msg.err
			m.message = "refresh failed: " + msg.err.Error()
			return m, setMessage(&m, "refresh failed: "+msg.err.Error())
		}
		if msg.final {
			m.loadingTr = false
			m.setPages(msg.snap)
			return m, nil
		}
		return m, waitForLoadEvent(m.loadCh)
	case pageLoadedMsg:
		m.loadingPage = false
		m.pageErr = msg.err
		if msg.err == nil {
			m.databaseID = ""
			m.databaseErr = nil
			m.activeID = msg.id
			m.activeTitle = msg.title
			m.pageCache[msg.id] = cachedPage{lastEdited: msg.lastEdited, markdown: msg.content}
			m.contentRaw = msg.content
			m.setContent(msg.content)
			m.focus = paneContent
			m.message = "page loaded"
			return m, setMessage(&m, "page loaded")
		}
		m.message = "page failed to load"
		return m, setMessage(&m, "page failed to load")
	case databaseLoadedMsg:
		m.databaseLoading = false
		m.databaseErr = msg.err
		if msg.err == nil {
			m.databaseID = msg.id
			m.databaseTable = msg.table
			m.activeID = msg.id
			m.activeTitle = msg.title
			m.contentRaw = ""
			m.setDatabaseContent()
			m.focus = paneContent
			m.message = "database loaded"
			return m, setMessage(&m, "database loaded")
		}
		m.message = "database failed to load"
		return m, setMessage(&m, "database failed to load")
	case editorDoneMsg:
		m.pageErr = msg.err
		if msg.err == nil && m.activeID != "" {
			m.loadingPage = true
			delete(m.pageCache, m.activeID)
			return m, fetchPageCmd(m.activeID, m.activeTitle, "")
		}
		return m, nil
	case clearStatusMsg:
		m.message = ""
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) setPages(snap cache.Snapshot) {
	m.roots = tree.Build(snap.Pages, snap.DataSources)
	m.rebuildRows()
}

func (m *Model) rebuildRows() {
	m.rows = nil
	q := strings.TrimSpace(m.filter)
	if q != "" {
		var walk func(nodes []*tree.Node)
		walk = func(nodes []*tree.Node) {
			for _, n := range nodes {
				if strings.Contains(strings.ToLower(n.Title()), strings.ToLower(q)) {
					m.rows = append(m.rows, flatRow{node: n, depth: 0})
				}
				walk(n.Children)
			}
		}
		walk(m.roots)
	} else {
		var walk func(nodes []*tree.Node, depth int)
		walk = func(nodes []*tree.Node, depth int) {
			for _, n := range nodes {
				m.rows = append(m.rows, flatRow{node: n, depth: depth})
				if m.expanded[n.ID] {
					walk(n.Children, depth+1)
				}
			}
		}
		walk(m.roots, 0)
	}
	if len(m.rows) == 0 {
		m.cursor = 0
		m.treeOffset = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	m.ensureCursorVisible()
}

func (m *Model) setContent(md string) {
	m.contentRaw = md
	rendered := md
	if m.renderer != nil {
		if out, err := m.renderer.Render(md); err == nil {
			rendered = out
		}
	}
	m.viewport.SetContent(rendered)
	m.viewport.GotoTop()
}

func (m *Model) setDatabaseContent() {
	m.viewport.SetContent(renderDatabaseTable(m.databaseTable, m.viewport.Width))
	m.viewport.GotoTop()
}

func (m *Model) layout() {
	cfg := calcLayout(m.width, m.height, 2, m.fullWidth)
	m.viewport.Width = max(cfg.contentWidth-4, 20)
	m.viewport.Height = max(cfg.bodyHeight-2, 1)
	if m.renderer != nil && m.viewport.Width > 0 {
		if r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(m.viewport.Width),
		); err == nil {
			m.renderer = r
		}
	}
	if m.contentRaw != "" {
		m.setContent(m.contentRaw)
	}
	if m.databaseID != "" {
		m.setDatabaseContent()
	}
	m.ensureCursorVisible()
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showHelp {
		if msg.String() == "?" || msg.String() == "esc" || msg.String() == "enter" {
			m.showHelp = false
		}
		return m, nil
	}
	if m.filtering {
		return m.handleFilterKey(msg)
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q":
		if m.focus == paneContent {
			m.focus = paneTree
			return m, nil
		}
		return m, tea.Quit
	case "?":
		m.showHelp = true
		return m, nil
	case "tab":
		if m.focus == paneTree {
			m.focus = paneContent
		} else {
			m.focus = paneTree
		}
		return m, nil
	case "r":
		m.loadingTr = true
		m.message = "refreshing..."
		for i := range m.stages {
			m.stages[i].done = false
			m.stages[i].active = false
			m.stages[i].detail = ""
		}
		m.stages[stageCache] = stageInfo{label: "Checking local cache", done: true, detail: "using cache while refreshing"}
		m.loadCh = make(chan loadEvent, 4)
		return m, startLoadCmd(m.loadCh)
	case "/":
		m.filtering = true
		m.message = ""
		return m, nil
	case "w":
		m.fullWidth = !m.fullWidth
		if m.fullWidth {
			m.focus = paneContent
		}
		m.layout()
		return m, nil
	}
	if m.focus == paneTree {
		return m.handleTreeKey(msg)
	}
	return m.handleContentKey(msg)
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.filtering = false
		m.filter = ""
		m.rebuildRows()
		return m, nil
	case tea.KeyEnter:
		m.filtering = false
		m.rebuildRows()
		return m, nil
	case tea.KeyBackspace:
		if len([]rune(m.filter)) > 0 {
			m.filter = string([]rune(m.filter)[:len([]rune(m.filter))-1])
		}
		m.rebuildRows()
		return m, nil
	case tea.KeyCtrlU:
		m.filter = ""
		m.rebuildRows()
		return m, nil
	case tea.KeyRunes:
		m.filter = sanitizeInput(m.filter + string(msg.Runes))
		m.rebuildRows()
		return m, nil
	}
	return m, nil
}

func (m Model) handleTreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		m.ensureCursorVisible()
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
		m.ensureCursorVisible()
	case "g":
		m.cursor = 0
		m.ensureCursorVisible()
	case "G":
		if len(m.rows) > 0 {
			m.cursor = len(m.rows) - 1
		}
		m.ensureCursorVisible()
	case "right", "l":
		if row := m.currentRow(); row != nil && len(row.node.Children) > 0 {
			m.expanded[row.node.ID] = true
			m.rebuildRows()
		}
	case "left", "h":
		if row := m.currentRow(); row != nil {
			if m.expanded[row.node.ID] {
				m.expanded[row.node.ID] = false
				m.rebuildRows()
			}
		}
	case "o":
		if row := m.currentRow(); row != nil && row.node.URL() != "" {
			return m, setMessage(&m, openInBrowser(row.node.URL()))
		}
	case "y":
		if row := m.currentRow(); row != nil {
			return m, setMessage(&m, copyToClipboard(row.node.ID))
		}
	case "enter":
		if row := m.currentRow(); row != nil {
			if row.node.IsContainer {
				m.expanded[row.node.ID] = true
				m.rebuildRows()
				m.pageErr = nil
				m.databaseErr = nil
				m.databaseLoading = true
				m.databaseID = row.node.ID
				m.activeID = row.node.ID
				m.activeTitle = row.node.Title()
				m.message = "loading database..."
				return m, fetchDatabaseCmd(row.node.ID, row.node.Title())
			}
			m.pageErr = nil
			m.databaseID = ""
			id, title := row.node.ID, row.node.Title()
			if cached, ok := m.pageCache[id]; ok && cached.lastEdited == row.node.LastEdited() {
				m.activeID = id
				m.activeTitle = title
				m.setContent(cached.markdown)
				m.focus = paneContent
				return m, nil
			}
			m.loadingPage = true
			m.message = "loading page..."
			return m, fetchPageCmd(id, title, row.node.LastEdited())
		}
	}
	return m, nil
}

func (m Model) handleContentKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "e":
		if m.activeID == "" || m.databaseID != "" {
			return m, nil
		}
		cmd := notion.EditPageInEditor(m.activeID)
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return editorDoneMsg{err: err}
		})
	case "esc":
		m.focus = paneTree
		return m, nil
	case "w":
		m.fullWidth = !m.fullWidth
		if m.fullWidth {
			m.focus = paneContent
		}
		m.layout()
		return m, nil
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) currentRow() *flatRow {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return &m.rows[m.cursor]
}

func (m *Model) ensureCursorVisible() {
	if len(m.rows) == 0 {
		m.treeOffset = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	h := max(m.height/3, 1)
	if m.cursor < m.treeOffset {
		m.treeOffset = m.cursor
	}
	if m.cursor >= m.treeOffset+h {
		m.treeOffset = m.cursor - h + 1
	}
	if m.treeOffset < 0 {
		m.treeOffset = 0
	}
	if m.treeOffset > len(m.rows)-h {
		m.treeOffset = max(len(m.rows)-h, 0)
	}
}

func calcLayout(width, height int, footerHeight int, fullWidth bool) layoutConfig {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	if footerHeight < 1 {
		footerHeight = 1
	}
	cfg := layoutConfig{footerHeight: footerHeight}
	cfg.bodyHeight = max(height-(footerHeight+3), 1)
	if fullWidth || width < 44 {
		cfg.treeWidth = 0
		cfg.contentWidth = max(width-2, 1)
		cfg.singlePane = true
		return cfg
	}
	cfg.treeWidth = clamp((width-6)/3, 18, 34)
	cfg.contentWidth = max(width-cfg.treeWidth-4, 1)
	return cfg
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func stripANSI(s string) string {
	re := regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")
	return re.ReplaceAllString(s, "")
}

func displayWidth(s string) int {
	return utf8.RuneCountInString(stripANSI(s))
}

func truncateDisplay(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if displayWidth(s) <= width {
		return s
	}
	plain := stripANSI(s)
	if width <= 1 {
		return "…"
	}
	trunc := truncate.String(plain, uint(width-1))
	if trunc == "" {
		return "…"
	}
	return trunc + "…"
}

func sanitizeInput(s string) string {
	return strings.TrimRightFunc(s, func(r rune) bool { return r == '\x00' })
}

func highlightMatch(text, q string) string {
	if strings.TrimSpace(q) == "" {
		return text
	}
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(q)
	idx := strings.Index(lowerText, lowerQuery)
	if idx < 0 {
		return text
	}
	runes := []rune(text)
	matchStart := idx
	matchEnd := idx + len([]rune(q))
	if matchEnd > len(runes) {
		matchEnd = len(runes)
	}
	var b strings.Builder
	b.WriteString(string(runes[:matchStart]))
	b.WriteString(lipgloss.NewStyle().
		Background(lipgloss.AdaptiveColor{Light: "#FDE68A", Dark: "#7C3AED"}).
		Foreground(lipgloss.AdaptiveColor{Light: "#111827", Dark: "#F8FAFC"}).
		Render(string(runes[matchStart:matchEnd])))
	b.WriteString(string(runes[matchEnd:]))
	return b.String()
}

func previewMetadata(md string) string {
	meta, _ := extractMetadataHeader(md)
	return meta
}

func extractMetadataHeader(md string) (string, string) {
	lines := strings.Split(md, "\n")
	meta := make([]string, 0, 4)
	seenBody := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(meta) > 0 {
				return strings.Join(meta, " • "), strings.Join(lines[i+1:], "\n")
			}
			if seenBody {
				return "", md
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "***") {
			if len(meta) > 0 {
				return strings.Join(meta, " • "), strings.Join(lines[i+1:], "\n")
			}
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, ">") {
			seenBody = true
			if len(meta) > 0 {
				return strings.Join(meta, " • "), strings.Join(lines[i:], "\n")
			}
			return "", md
		}
		if strings.Contains(trimmed, ":") {
			meta = append(meta, trimmed)
			continue
		}
		seenBody = true
		if len(meta) > 0 {
			return strings.Join(meta, " • "), strings.Join(lines[i:], "\n")
		}
		return "", md
	}
	if len(meta) == 0 {
		return "", md
	}
	return strings.Join(meta, " • "), ""
}

func parentPathForNode(node *tree.Node, roots []*tree.Node) string {
	if node == nil {
		return ""
	}
	parent := findParent(node.ID, roots)
	if parent == nil {
		return ""
	}
	parts := []string{parent.Title()}
	for grand := findParent(parent.ID, roots); grand != nil; grand = findParent(grand.ID, roots) {
		parts = append([]string{grand.Title()}, parts...)
	}
	return strings.Join(parts, " / ")
}

func findParent(nodeID string, roots []*tree.Node) *tree.Node {
	for _, root := range roots {
		if root == nil {
			continue
		}
		for _, child := range root.Children {
			if child != nil && child.ID == nodeID {
				return root
			}
			if p := findParent(nodeID, []*tree.Node{child}); p != nil {
				return p
			}
		}
	}
	return nil
}

func previewBreadcrumb(m Model) string {
	if m.activeID == "" {
		return ""
	}
	for _, row := range m.rows {
		if row.node != nil && row.node.ID == m.activeID {
			if path := parentPathForNode(row.node, m.roots); path != "" {
				return path
			}
		}
	}
	return ""
}

func renderPreviewHeader(m Model, width int) string {
	title := m.activeTitle
	if title == "" {
		title = "Untitled page"
	}
	titleLine := lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render(title)
	parts := make([]string, 0, 2)
	if path := previewBreadcrumb(m); path != "" {
		parts = append(parts, dimStyle.Render(path))
	}
	if meta := previewMetadata(m.contentRaw); meta != "" {
		parts = append(parts, dimStyle.Render(meta))
	}
	details := strings.Join(parts, " • ")
	if displayWidth(title) > width-4 {
		titleLine = lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render(truncateDisplay(title, width-4))
	}
	if details == "" {
		return lipgloss.NewStyle().Width(width).MaxWidth(width).Padding(0, 1).Render(titleLine)
	}
	if displayWidth(details) > width-4 {
		details = truncateDisplay(details, width-4)
	}
	header := titleLine + "\n" + dimStyle.Render(details)
	return lipgloss.NewStyle().Width(width).MaxWidth(width).Padding(0, 1).Render(header)
}

func previewScrollIndicator(m Model) string {
	pct := m.viewport.ScrollPercent()
	switch {
	case pct <= 0.05:
		return "Top"
	case pct >= 0.95:
		return "Bottom"
	default:
		return fmt.Sprintf("%d%%", int(pct*100))
	}
}

func renderPaneHeader(title string, active bool, meta string, width int) string {
	label := title
	if active {
		label = lipgloss.NewStyle().Foreground(accentColor).Render("● ") + title
	}
	if meta != "" {
		label = label + " " + dimStyle.Render(meta)
	}
	return lipgloss.NewStyle().Width(width).MaxWidth(width).Padding(0, 1).Bold(true).Render(dimStyle.Render(label))
}

func renderHelpOverlay(width, height int) string {
	body := strings.Join([]string{
		"Navigation",
		"  ↑/↓ or j/k move      h/l fold/unfold    / filter        enter open page",
		"  tab switch panes     e edit in $EDITOR  w toggle full-width",
		"  o open in browser    y copy page id     r refresh tree",
		"",
		"Keys",
		"  ? or Esc close        q from preview → tree    q from tree quits",
		"  Ctrl+u clear filter   Enter apply filter    Esc cancel filter",
	}, "\n")
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Padding(1, 2).
		Width(min(width-8, 90)).
		Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (m Model) renderLoading() string {
	var b strings.Builder
	for i := range m.stages {
		s := m.stages[i]
		switch {
		case s.done:
			b.WriteString(doneStyle.Render("✓ " + s.label))
		case s.active:
			b.WriteString(m.spinner.View() + " " + s.label)
		default:
			b.WriteString(dimStyle.Render("  " + s.label))
		}
		if s.detail != "" {
			b.WriteString(dimStyle.Render(" — " + s.detail))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) renderTree(height int) string {
	if m.loadingTr && len(m.rows) == 0 {
		return m.renderLoading()
	}
	if m.treeErr != nil && len(m.rows) == 0 {
		return errStyle.Render("error: " + m.treeErr.Error())
	}
	if len(m.rows) == 0 {
		if m.filter != "" {
			return dimStyle.Render("No matching pages")
		}
		return dimStyle.Render("No pages found")
	}

	var b strings.Builder
	end := min(m.treeOffset+height, len(m.rows))
	for i := m.treeOffset; i < end; i++ {
		row := m.rows[i]
		indent := strings.Repeat("  ", row.depth)
		marker := " "
		if len(row.node.Children) > 0 {
			if m.expanded[row.node.ID] {
				marker = "▾"
			} else {
				marker = "▸"
			}
		}
		if row.node.ID == m.activeID {
			marker = "●"
		}
		title := row.node.Title()
		if m.filter != "" {
			title = highlightMatch(title, m.filter)
			if path := parentPathForNode(row.node, m.roots); path != "" {
				title = title + " " + dimStyle.Render("("+path+")")
			}
		}
		if row.node.IsContainer {
			title = containerStyle.Render("▤ " + row.node.Title())
		}
		line := indent + marker + " " + title
		if i == m.cursor {
			line = selectedStyle.Render("▌ " + line)
		}
		line = truncateDisplay(line, max(m.width/3, 18))
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) renderContent(height int) string {
	if m.loadingPage {
		return m.spinner.View() + " loading page..."
	}
	if m.databaseLoading {
		return m.spinner.View() + " loading database..."
	}
	if m.pageErr != nil {
		return errStyle.Render("error: " + m.pageErr.Error())
	}
	if m.databaseErr != nil {
		return errStyle.Render("error: " + m.databaseErr.Error())
	}
	if m.activeID == "" {
		return dimStyle.Render("Select a page and press Enter to view it.")
	}
	if m.viewport.Height > 0 {
		m.viewport.Height = height
	}
	return m.viewport.View()
}

func renderDatabaseTable(table notion.DataSourceTable, width int) string {
	if len(table.Columns) == 0 {
		return dimStyle.Render("No database rows found.")
	}
	if width < 1 {
		width = 1
	}
	widths := make([]int, len(table.Columns))
	for i, column := range table.Columns {
		widths[i] = min(max(displayWidth(column), 4), 24)
		for _, row := range table.Rows {
			if i < len(row) {
				widths[i] = min(max(widths[i], displayWidth(row[i])), 24)
			}
		}
	}
	separatorWidth := max(len(widths)-1, 0) * 3
	for totalWidth(widths)+separatorWidth > width {
		longest := 0
		for i := range widths {
			if widths[i] > widths[longest] {
				longest = i
			}
		}
		if widths[longest] <= 4 {
			break
		}
		widths[longest]--
	}

	lines := []string{databaseTableLine(table.Columns, widths), databaseTableRule(widths)}
	for _, row := range table.Rows {
		values := make([]string, len(table.Columns))
		for i := range values {
			if i < len(row) {
				values[i] = strings.ReplaceAll(strings.ReplaceAll(row[i], "\n", " "), "\r", " ")
			}
		}
		lines = append(lines, databaseTableLine(values, widths))
	}
	return strings.Join(lines, "\n")
}

func totalWidth(widths []int) int {
	total := 0
	for _, width := range widths {
		total += width
	}
	return total
}

func databaseTableLine(values []string, widths []int) string {
	parts := make([]string, len(widths))
	for i, width := range widths {
		value := ""
		if i < len(values) {
			value = values[i]
		}
		parts[i] = truncateDisplay(value, width)
		parts[i] += strings.Repeat(" ", max(width-displayWidth(parts[i]), 0))
	}
	return strings.Join(parts, " │ ")
}

func databaseTableRule(widths []int) string {
	parts := make([]string, len(widths))
	for i, width := range widths {
		parts[i] = strings.Repeat("─", width)
	}
	return strings.Join(parts, "─┼─")
}

func (m Model) renderScrollBar(height int) string {
	if height <= 0 {
		return ""
	}
	total := m.viewport.TotalLineCount()
	thumbHeight := height
	if total > height {
		thumbHeight = max(height*height/total, 1)
	}
	start := 0
	if total > height {
		start = int(float64(height-thumbHeight) * m.viewport.ScrollPercent())
	}
	lines := make([]string, height)
	for i := range lines {
		lines[i] = scrollTrackStyle.Render("│")
		if i >= start && i < start+thumbHeight {
			lines[i] = scrollThumbStyle.Render("┃")
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderStatus() string {
	if m.filtering {
		return statusStyle.Render("filter: " + m.filter + "  [enter apply • esc cancel • ctrl+u clear]")
	}
	base := "↑/↓ move • / filter • enter open • w full-width • ? help • q quit"
	if m.focus == paneContent {
		base = "e edit • esc back • w full-width • ? help • q quit"
		if m.databaseID != "" {
			base = "database (read-only) • esc back • w full-width • ? help • q quit"
		}
	}
	if m.message != "" {
		base = m.message + "  •  " + base
	}
	if m.filter != "" {
		base = fmt.Sprintf("%d matches • %s", len(m.rows), base)
	}
	return statusStyle.Padding(0, 1).BorderTop(true).BorderForeground(mutedFG).Render(base)
}

func (m Model) View() string {
	cfg := calcLayout(m.width, m.height, 2, m.fullWidth)
	treeStyle := paneBorder
	contentStyle := paneBorder
	if m.focus == paneTree {
		treeStyle = activeBorder
	} else {
		contentStyle = activeBorder
	}

	contentBodyHeight := cfg.bodyHeight - 3
	contentView := lipgloss.JoinHorizontal(lipgloss.Top,
		m.renderContent(contentBodyHeight),
		m.renderScrollBar(contentBodyHeight),
	)
	contentPane := contentStyle.Width(cfg.contentWidth).Height(cfg.bodyHeight).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			renderPreviewHeader(m, cfg.contentWidth),
			contentView,
		),
	)

	body := contentPane
	if !cfg.singlePane {
		treePane := treeStyle.Width(cfg.treeWidth).Height(cfg.bodyHeight).Render(
			lipgloss.JoinVertical(lipgloss.Left,
				renderPaneHeader("Pages", m.focus == paneTree, fmt.Sprintf("%d visible", len(m.rows)), cfg.treeWidth),
				m.renderTree(cfg.bodyHeight-2),
			),
		)
		body = lipgloss.JoinHorizontal(lipgloss.Top, treePane, contentPane)
	}
	view := lipgloss.JoinVertical(lipgloss.Left, body, m.renderStatus())
	if m.showHelp {
		return view + "\n" + renderHelpOverlay(m.width, m.height)
	}
	return view
}

func openInBrowser(url string) string {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "start"
	default:
		cmd = "xdg-open"
	}
	if err := exec.Command(cmd, url).Start(); err != nil {
		return "error opening browser: " + err.Error()
	}
	return "opened in browser"
}

func copyToClipboard(text string) string {
	candidates := [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}, {"pbcopy"}}
	for _, args := range candidates {
		if _, err := exec.LookPath(args[0]); err != nil {
			continue
		}
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			return "error copying: " + err.Error()
		}
		return "copied page id to clipboard"
	}
	return "no clipboard tool found"
}

func setMessage(m *Model, msg string) tea.Cmd {
	m.message = msg
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearStatusMsg{} })
}

var (
	paneBorder       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.AdaptiveColor{Light: "#94A3B8", Dark: "#64748B"})
	activeBorder     = lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).BorderForeground(lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#7DD3FC"})
	selectedFG       = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#F8FAFC"}
	selectedBG       = lipgloss.AdaptiveColor{Light: "#E0E7FF", Dark: "#1D4ED8"}
	mutedFG          = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#94A3B8"}
	statusFG         = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#E2E8F0"}
	errFG            = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#FCA5A5"}
	doneFG           = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#86EFAC"}
	containerFG      = lipgloss.AdaptiveColor{Light: "#4F46E5", Dark: "#A5B4FC"}
	accentColor      = lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#7DD3FC"}
	selectedStyle    = lipgloss.NewStyle().Background(selectedBG).Foreground(selectedFG).Bold(true)
	statusStyle      = lipgloss.NewStyle().Foreground(statusFG)
	errStyle         = lipgloss.NewStyle().Foreground(errFG)
	dimStyle         = lipgloss.NewStyle().Foreground(mutedFG)
	doneStyle        = lipgloss.NewStyle().Foreground(doneFG)
	containerStyle   = lipgloss.NewStyle().Foreground(containerFG)
	scrollTrackStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#CBD5E1", Dark: "#475569"})
	scrollThumbStyle = lipgloss.NewStyle().Foreground(accentColor)
)
