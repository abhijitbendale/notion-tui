# notion-tui plan and status

## Current status

The project is implemented as a Go + Bubble Tea TUI around the existing `ntn` CLI workflow. The app keeps the left-page tree / right-preview layout, caches page trees to improve startup time, and delegates editing to `$EDITOR` via `ntn pages edit`.

## Completed work

- bubbletea app shell and Notion CLI integration are in place
- cached startup tree and background refresh flow remain intact
- page hierarchy is built around `page_id` and `data_source_id` relationships
- keyboard navigation is preserved for tree browsing, filtering, and viewing
- the UI has been polished for better readability and usability, with focused pane styling, filter highlighting, help overlay, and full-width preview toggle
- layout math now guards against narrow terminals and keeps widths within sensible limits
- preview headers now include a compact breadcrumb and metadata summary when present
- status messages clear automatically and refresh failures remain visible even when cached data is still displayed

## Remaining limitations

- live Notion CLI testing is still dependent on a real `ntn` login and accessible workspace
- the preview metadata extraction is intentionally conservative and only recognizes common label/value document headers
- the TUI remains a terminal app; very large content still depends on the existing viewport and glamour renderer
