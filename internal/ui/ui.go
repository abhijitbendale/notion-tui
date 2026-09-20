// Package ui implements the Bubble Tea model for notion-tui: a left tree pane
// and a right content pane, with on-demand page fetch and editor handoff.
package ui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

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

// Model is the top-level Bubble Tea model.
type Model struct {
	width, height int
	focus         pane

	roots      []*tree.Node
	expanded   map[string]bool
	rows       []flatRow
	cursor     int
	treeOffset int // index of the first visible row, for scrolling

	spinner   spinner.Model
	loadingTr bool
	treeErr   error
	stages    [numStages]stageInfo
	loadCh    chan loadEvent

	viewport    viewport.Model
	loadingPage bool
	pageErr     error
	activeID    string
	activeTitle string
	renderer    *glamour.TermRenderer
	pageCache   map[string]cachedPage // page id -> last-fetched content, keyed by last_edited_time

	filtering bool
	filter    string
	showHelp  bool

	message string // transient status-bar message (e.g. "copied", "opened in browser")
}

type cachedPage struct {
	lastEdited string
	markdown   string
}

// New creates the initial model. Tree loading is kicked off as a Bubble Tea command.
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
		width:     80, // sane defaults so the loading view renders before the first WindowSizeMsg arrives
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

// --- messages ---

type cachedTreeMsg struct{ snap cache.Snapshot }

// loadEvent reports progress from the background fetch goroutine as it works
// through cache -> pages -> databases -> tree-build, so the UI can show which
// stage is active and how long each one took.
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
type editorDoneMsg struct{ err error }

func loadCachedTreeCmd() tea.Msg {
	snap, err := cache.Load()
	if err != nil || len(snap.Pages) == 0 {
		return nil
	}
	return cachedTreeMsg{snap: snap}
}

// startLoadCmd launches the background fetch goroutine and returns a command
// that waits for its first progress event.
func startLoadCmd(ch chan loadEvent) tea.Cmd {
	return func() tea.Msg {
		go runLoad(ch)
		return <-ch
	}
}

func waitForLoadEvent(ch chan loadEvent) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// runLoad fetches pages and databases from Notion, timing each stage, and
// streams progress events to ch. The last event sent is always final (err or ok).
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

// --- update ---

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
		m.stages[stageCache] = stageInfo{
			label: "Checking local cache", done: true,
			detail: fmt.Sprintf("%d pages from last run", len(msg.snap.Pages)),
		}
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
			return m, nil
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
			m.activeID = msg.id
			m.activeTitle = msg.title
			m.pageCache[msg.id] = cachedPage{lastEdited: msg.lastEdited, markdown: msg.content}
			m.setContent(msg.content)
			m.focus = paneContent
		}
		return m, nil

	case editorDoneMsg:
		m.pageErr = msg.err
		if msg.err == nil && m.activeID != "" {
			m.loadingPage = true
			delete(m.pageCache, m.activeID) // force refetch since the editor may have changed it
			return m, fetchPageCmd(m.activeID, m.activeTitle, "")
		}
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

	if q := strings.ToLower(strings.TrimSpace(m.filter)); q != "" {
		// Filter mode: flatten every node (ignoring expand/collapse) that matches.
		var walk func(nodes []*tree.Node)
		walk = func(nodes []*tree.Node) {
			for _, n := range nodes {
				if strings.Contains(strings.ToLower(n.Title()), q) {
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

	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.ensureCursorVisible()
}

// treeBodyHeight returns how many tree rows fit in the pane, mirroring the
// layout math in View().
func (m *Model) treeBodyHeight() int {
	h := m.height - 4
	if h < 1 {
		h = 1
	}
	return h
}

// ensureCursorVisible scrolls the tree pane so the cursor row stays on screen.
func (m *Model) ensureCursorVisible() {
	h := m.treeBodyHeight()
	if m.cursor < m.treeOffset {
		m.treeOffset = m.cursor
	}
	if m.cursor >= m.treeOffset+h {
		m.treeOffset = m.cursor - h + 1
	}
	if m.treeOffset < 0 {
		m.treeOffset = 0
	}
}

func (m *Model) setContent(md string) {
	rendered := md
	if m.renderer != nil {
		if out, err := m.renderer.Render(md); err == nil {
			rendered = out
		}
	}
	m.viewport.SetContent(rendered)
	m.viewport.GotoTop()
}

func (m *Model) layout() {
	treeWidth := m.width / 3
	if treeWidth < 20 {
		treeWidth = 20
	}
	contentWidth := m.width - treeWidth - 3
	bodyHeight := m.height - 2 // status bar + margin

	m.viewport.Width = contentWidth
	m.viewport.Height = bodyHeight

	if m.renderer != nil && contentWidth > 0 {
		if r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(contentWidth),
		); err == nil {
			m.renderer = r
		}
	}

	m.ensureCursorVisible()
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		m.showHelp = !m.showHelp
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
		m.message = ""
		for i := range m.stages {
			m.stages[i].done = false
			m.stages[i].active = false
			m.stages[i].detail = ""
		}
		m.stages[stageCache] = stageInfo{label: "Checking local cache", done: true, detail: "skipped (manual refresh)"}
		m.loadCh = make(chan loadEvent, 4)
		return m, startLoadCmd(m.loadCh)
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
	case tea.KeyEnter:
		m.filtering = false
	case tea.KeyBackspace:
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
		}
		m.rebuildRows()
	case tea.KeyRunes:
		m.filter += string(msg.Runes)
		m.rebuildRows()
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
		m.cursor = len(m.rows) - 1
		m.ensureCursorVisible()
	case "/":
		m.filtering = true
		m.filter = ""
		m.message = ""
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
			m.message = openInBrowser(row.node.URL())
		}
	case "y":
		if row := m.currentRow(); row != nil {
			m.message = copyToClipboard(row.node.ID)
		}
	case "enter":
		if row := m.currentRow(); row != nil {
			if row.node.IsContainer {
				// Databases aren't fetchable as Markdown pages; enter just expands them.
				m.expanded[row.node.ID] = !m.expanded[row.node.ID]
				m.rebuildRows()
				return m, nil
			}
			m.pageErr = nil
			id, title := row.node.ID, row.node.Title()
			if cached, ok := m.pageCache[id]; ok && cached.lastEdited == row.node.LastEdited() {
				m.activeID = id
				m.activeTitle = title
				m.setContent(cached.markdown)
				m.focus = paneContent
				return m, nil
			}
			m.loadingPage = true
			return m, fetchPageCmd(id, title, row.node.LastEdited())
		}
	}
	return m, nil
}

func (m Model) handleContentKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "e":
		if m.activeID == "" {
			return m, nil
		}
		cmd := notion.EditPageInEditor(m.activeID)
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return editorDoneMsg{err: err}
		})
	case "esc":
		m.focus = paneTree
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

// --- view ---

var (
	treeBorderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	activeBorder    = treeBorderStyle.BorderForeground(lipgloss.Color("212"))
	selectedStyle   = lipgloss.NewStyle().Reverse(true)
	statusStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	doneStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	containerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
)

func (m Model) View() string {
	treeWidth := m.width/3 - 2
	if treeWidth < 18 {
		treeWidth = 18
	}
	bodyHeight := m.height - 4

	treeStyle := treeBorderStyle
	contentStyle := treeBorderStyle
	if m.focus == paneTree {
		treeStyle = activeBorder
	} else {
		contentStyle = activeBorder
	}

	treePane := treeStyle.Width(treeWidth).Height(bodyHeight).Render(m.renderTree(bodyHeight))
	contentPane := contentStyle.Width(m.width - treeWidth - 6).Height(bodyHeight).Render(m.renderContent())

	body := lipgloss.JoinHorizontal(lipgloss.Top, treePane, contentPane)
	view := lipgloss.JoinVertical(lipgloss.Left, body, m.renderStatus())
	if m.showHelp {
		return view + "\n" + m.renderHelp()
	}
	return view
}

func (m Model) renderHelp() string {
	lines := []string{
		"j/k, ↑/↓  move       enter  open page       tab  switch pane",
		"h/l, ←/→  collapse/expand    g/G  top/bottom      /  filter",
		"e  edit in $EDITOR (content pane)             o  open in browser",
		"y  copy page id      r  refresh tree          ?  toggle this help",
		"q  back/quit",
	}
	return statusStyle.Render(strings.Join(lines, "\n"))
}

// renderLoading shows a step-by-step progress list (cache -> pages -> databases
// -> tree build) so the user can see which stage is active and how long
// previous stages took, instead of a bare "loading..." message.
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
		return "(no pages found)"
	}

	var b strings.Builder
	end := m.treeOffset + height
	if end > len(m.rows) {
		end = len(m.rows)
	}
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
		title := row.node.Title()
		if row.node.IsContainer {
			title = containerStyle.Render("▤ " + title)
		}
		line := fmt.Sprintf("%s%s %s", indent, marker, title)
		if i == m.cursor {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) renderContent() string {
	if m.loadingPage {
		return m.spinner.View() + " loading page..."
	}
	if m.pageErr != nil {
		return errStyle.Render("error: " + m.pageErr.Error())
	}
	if m.activeID == "" {
		return "Select a page and press Enter to view it."
	}
	return m.viewport.View()
}

func (m Model) renderStatus() string {
	if m.filtering {
		return statusStyle.Render("filter: " + m.filter + "█   (enter: apply  esc: cancel)")
	}

	hint := "j/k move  h/l fold  g/G top/bot  /  filter  enter open  o browser  y copy id  tab switch  ? help  q quit"
	if m.focus == paneContent {
		hint = "e edit in $EDITOR  esc/tab back to tree  ? help  q quit"
	}
	if m.message != "" {
		hint = m.message + "   (" + hint + ")"
	}
	return statusStyle.Render(hint)
}

// openInBrowser opens a URL with the OS-appropriate opener and returns a status message.
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

// copyToClipboard copies text using the first available clipboard tool and returns a status message.
func copyToClipboard(text string) string {
	candidates := [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
		{"pbcopy"},
	}
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
	return "no clipboard tool found (install xclip/xsel/wl-copy)"
}
