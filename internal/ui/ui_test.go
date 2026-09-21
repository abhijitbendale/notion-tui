package ui

import (
	"errors"
	"strings"
	"testing"
)

func TestCalcLayoutRespectsBounds(t *testing.T) {
	layout := calcLayout(120, 30, 0, false)
	if layout.treeWidth <= 0 || layout.contentWidth <= 0 {
		t.Fatalf("expected positive panes, got %+v", layout)
	}
	if layout.treeWidth > 40 {
		t.Fatalf("tree width should be capped on wide terminals, got %d", layout.treeWidth)
	}
	if layout.footerHeight < 1 {
		t.Fatalf("expected footer height >= 1, got %d", layout.footerHeight)
	}
}

func TestCalcLayoutHandlesSmallTerminal(t *testing.T) {
	layout := calcLayout(40, 10, 0, false)
	if !layout.singlePane || layout.treeWidth != 0 {
		t.Fatalf("expected single-pane layout on a small terminal, got %+v", layout)
	}
	if layout.bodyHeight < 1 {
		t.Fatalf("expected body height >= 1, got %d", layout.bodyHeight)
	}
}

func TestExtractMetadataHeader(t *testing.T) {
	md := "# Project\n\nCreated by: a@example.com\nLast edited: 2024-01-01\n\n---\n\nHello"
	meta, _ := extractMetadataHeader(md)
	if meta == "" {
		t.Fatal("expected metadata header to be extracted")
	}
	if len(meta) > 120 {
		t.Fatalf("metadata header should be compact, got %q", meta)
	}
}

func TestNormalizeNotionMarkdownUnwrapsSyncedBlocks(t *testing.T) {
	md := "## Questions\n\n<synced_block_reference url=\"https://example.test\">\n        ```json\n- First question\n- Second question\n        ```\n</synced_block_reference>\n\n<empty-block/>"
	got := normalizeNotionMarkdown(md)
	want := "## Questions\n\n- First question\n- Second question\n"
	if got != want {
		t.Fatalf("unexpected normalized Markdown:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestNormalizeNotionMarkdownPreservesRegularCodeBlocks(t *testing.T) {
	md := "```json\n{\"keep\": true}\n```"
	if got := normalizeNotionMarkdown(md); got != md {
		t.Fatalf("regular code block was changed: %q", got)
	}
}

func TestScrollBarUsesViewportPosition(t *testing.T) {
	m := New()
	m.viewport.Height = 4
	m.viewport.SetContent("one\ntwo\nthree\nfour\nfive\nsix\nseven\neight")
	m.viewport.GotoBottom()

	bar := m.renderScrollBar(4)
	if bar == "" || !strings.Contains(bar, "┃") {
		t.Fatalf("expected visible scrollbar thumb, got %q", bar)
	}
}

func TestFilterHighlight(t *testing.T) {
	got := highlightMatch("Project Notes", "notes")
	if stripANSI(got) != "Project Notes" || got == "" {
		t.Fatalf("expected highlighted text to preserve content, got %q", got)
	}
}

func TestEditorExitReloadsPageEvenWhenEditorReturnsError(t *testing.T) {
	m := New()
	m.activeID = "page-1"
	m.activeTitle = "Example"
	m.contentRaw = "# Example"

	updated, cmd := m.Update(editorDoneMsg{err: errors.New("exit status 5")})
	model := updated.(Model)
	if cmd == nil {
		t.Fatal("expected page reload command")
	}
	if !model.loadingPage {
		t.Fatal("expected page reload to be marked as loading")
	}
	if model.pageErr != nil {
		t.Fatalf("editor exit should not replace the page with an error: %v", model.pageErr)
	}
}
