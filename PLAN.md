# notion-tui — Design Plan

## Language choice: **Go**

Recommended over Python/Rust/Node for this project:

- **Bubble Tea** (`charmbracelet/bubbletea`) + **Bubbles** (tree/list/viewport widgets) +
  **Lipgloss** (styling) is the most mature, batteries-included TUI stack available today.
  Building a page-tree browser + status/preview panes is mostly wiring these together.
- Single static binary, trivial to distribute (`go install`, no venv/node_modules).
- Fast startup — important since this runs as a "launch it and browse" tool.
- Easy to shell out to `ntn` and to `$EDITOR` (`os/exec`, inherit stdio) and wait for exit,
  which is exactly the "drop to emacs, then resume TUI" flow you want.
- Good JSON handling in the standard library for parsing `ntn api` / `ntn pages get --json` output.

Runner-up: **Python + Textual** — also excellent (arguably nicer widget model, reactive
CSS-like styling, async built in), and "simple" in the sense of faster iteration/scripting.
Worth it if you're more comfortable in Python or want to prototype fastest. Trade-off: needs
a Python env/venv to distribute, slightly slower cold start.

Not recommended: raw ncurses/termbox in C, or Node/Ink (extra runtime dependency, more
plumbing for subprocess + terminal handoff to an editor).

**Suggestion:** Go + Bubble Tea, unless you'd rather prototype in Python/Textual first.

## Relevant `ntn` commands (confirmed via `ntn --help`, `ntn pages --help`, `ntn api --help`)

- `ntn api v1/search filter:='{"property":"object","value":"page"}' page_size:=100`
  → flat list of pages, each with `id`, `properties.title`, and **`parent.page_id`**
  (or `parent.workspace`/`parent.database_id`). This is enough to reconstruct the tree
  client-side (build a map id→node, attach each node under `parent.page_id`, roots = pages
  whose parent is not itself another page in the set, e.g. `parent.workspace == true`).
  Use `page_size` + `next_cursor`/`has_more` to paginate for large workspaces.
- `ntn pages get <page-id> [--json]` → retrieves a single page as Markdown (with frontmatter),
  or full JSON with block content. This is the "only fetch on Enter" call.
- `ntn pages edit <page-id> [--content <md> | stdin]` → opens `$EDITOR`/`$VISUAL` interactively
  if no content source given — this is exactly the "shell out to emacs" hook.
- `ntn pages create --parent page:<id>|database:<id>|data-source:<id>` — optional "new page" action.
- `ntn pages trash <page-id>` — optional "delete" action.
- Auth is already handled by `ntn` (keychain/`NOTION_API_TOKEN`); the TUI doesn't need to
  manage tokens itself, just needs `ntn` on `$PATH` and already logged in.

## Functional plan

### 1. Startup
- Run `ntn api v1/search ...` (paginating on `has_more`) once at launch, in the background,
  showing a spinner/loading state.
- Parse JSON, build an in-memory tree (id → {title, parent_id, children[]}).
- Cache the tree + a timestamp to disk (e.g. `~/.cache/notion-tui/tree.json`) so subsequent
  launches can render instantly while a background refresh runs (stale-while-revalidate).

### 2. Browsing
- Left pane: collapsible tree view of pages (root pages expanded by default, or all
  collapsed with `→`/`l` to expand, `←`/`h` to collapse, `↑↓`/`j k` to move, `/` to
  fuzzy-search/filter by title across the whole tree).
- Right pane / bottom: preview info only from cached metadata (title, last-edited, URL) —
  **no content fetch** until the user acts.
- Status bar: keybinding hints, loading/error state.

### 3. Viewing a page (Enter)
- On `Enter`, shell out to `ntn pages get <id> --json` (or plain markdown), show a spinner,
  then render the Markdown content in a scrollable viewport pane (or full-screen view).
- Cache fetched page content in memory (and optionally on disk) keyed by id + `last_edited_time`,
  so re-opening the same page without edits doesn't re-fetch.

### 4. Editing (`e`)
- Suspend the TUI (Bubble Tea supports `tea.ExecProcess`), run
  `ntn pages edit <id>` with stdio inherited so `$EDITOR` (emacs) takes over the terminal.
- On editor exit, resume the TUI, refresh that page's node (re-fetch to confirm save,
  update `last_edited_time` in the tree/cache).

### 5. Other actions (nice-to-have, later)
- `n` — create a new child page under the selected node (`ntn pages create --parent page:<id>`,
  opens `$EDITOR` for content).
- `d` — trash a page (`ntn pages trash <id>`), with a confirm prompt.
- `r` — manual full refresh of the tree from the server.
- `y` — yank the page URL/id to clipboard.
- `o` — open the page URL in the browser.

### 6. Error/edge handling
- `ntn` not installed / not logged in → surface `ntn doctor`/`ntn whoami` guidance.
- Network errors on fetch/edit → show inline error, don't crash the TUI.
- Pages whose parent isn't in the fetched set (e.g. lives in a database, or search page_size
  cutoff) → bucket under an "Other / Unfiled" root instead of dropping them.

## Suggested project layout (Go)

```
notion-tui/
  main.go            # entrypoint, bubbletea program setup
  internal/
    notion/          # thin wrapper around exec.Command("ntn", ...) + JSON structs
    tree/            # build tree from flat search results
    ui/              # bubbletea model/update/view, tree widget, viewport, editor launcher
    cache/           # disk cache read/write
  go.mod
```

## Open questions for you
1. Go + Bubble Tea, or would you rather prototype in Python + Textual first?
2. Should pages living inside databases (not plain page search results) be in scope, or
   pages-only for v1?
3. Any preference on cache location/TTL for the background refresh?
