// Package notion wraps the `ntn` CLI as subprocess calls.
package notion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// Page is a single result from the Notion search API, as returned by `ntn api v1/search`.
type Page struct {
	ID         string     `json:"id"`
	Object     string     `json:"object"`
	URL        string     `json:"url"`
	Parent     PageParent `json:"parent"`
	Properties Properties `json:"properties"`
	LastEdited string     `json:"last_edited_time"`
	Archived   bool       `json:"is_archived"`
	InTrash    bool       `json:"in_trash"`
}

// PageParent describes a page's parent reference (page, database, data_source, or workspace).
type PageParent struct {
	Type         string `json:"type"`
	PageID       string `json:"page_id"`
	DatabaseID   string `json:"database_id"`
	DataSourceID string `json:"data_source_id"`
	Workspace    bool   `json:"workspace"`
}

// Properties holds the subset of page properties we care about (just the title).
type Properties struct {
	Title TitleProperty `json:"title"`
}

// TitleProperty is Notion's rich-text title property shape.
type TitleProperty struct {
	Title []RichText `json:"title"`
}

// RichText is a single rich-text fragment.
type RichText struct {
	PlainText string `json:"plain_text"`
}

// Title returns the page's plain-text title, or "Untitled" if empty.
func (p Page) Title() string {
	return richTextTitle(p.Properties.Title.Title)
}

// DataSource is a database's schema/rows container, as returned by
// `ntn api v1/search filter:={"property":"object","value":"data_source"}`.
// Notion databases show up in the sidebar anchored to a page (DatabaseParent);
// rows (pages) inside them have parent.type == "data_source_id" pointing here.
type DataSource struct {
	ID             string     `json:"id"`
	URL            string     `json:"url"`
	TitleRT        []RichText `json:"title"`
	Parent         PageParent `json:"parent"`          // the database "block" this schema belongs to
	DatabaseParent PageParent `json:"database_parent"` // the page/workspace the database is anchored to
}

// Title returns the data source's plain-text title, or "Untitled" if empty.
func (d DataSource) Title() string {
	return richTextTitle(d.TitleRT)
}

func richTextTitle(rt []RichText) string {
	var sb bytes.Buffer
	for _, r := range rt {
		sb.WriteString(r.PlainText)
	}
	if sb.Len() == 0 {
		return "Untitled"
	}
	return sb.String()
}

type searchResponse struct {
	Results    json.RawMessage `json:"results"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor"`
}

// ProgressFunc is called after each page of results is fetched, with the
// running total fetched so far. Used to drive a UI progress indicator.
type ProgressFunc func(fetched int)

// SearchAllPages fetches every page visible to the integration, paginating via next_cursor.
func SearchAllPages(onProgress ProgressFunc) ([]Page, error) {
	var all []Page
	err := searchAll(`{"property":"object","value":"page"}`, func(raw json.RawMessage) (int, error) {
		var results []Page
		if err := json.Unmarshal(raw, &results); err != nil {
			return 0, err
		}
		all = append(all, results...)
		return len(all), nil
	}, onProgress)
	return all, err
}

// SearchAllDataSources fetches every database (data source) visible to the
// integration, paginating via next_cursor.
func SearchAllDataSources(onProgress ProgressFunc) ([]DataSource, error) {
	var all []DataSource
	err := searchAll(`{"property":"object","value":"data_source"}`, func(raw json.RawMessage) (int, error) {
		var results []DataSource
		if err := json.Unmarshal(raw, &results); err != nil {
			return 0, err
		}
		all = append(all, results...)
		return len(all), nil
	}, onProgress)
	return all, err
}

// searchAll runs `ntn api v1/search` with the given filter, paginating via
// next_cursor and decoding each page of raw results through decode.
func searchAll(filter string, decode func(json.RawMessage) (int, error), onProgress ProgressFunc) error {
	cursor := ""
	for {
		args := []string{"api", "v1/search",
			"filter:=" + filter,
			"page_size:=100",
		}
		if cursor != "" {
			args = append(args, fmt.Sprintf("start_cursor=%s", cursor))
		}

		out, err := runNtn(args...)
		if err != nil {
			return err
		}

		var resp searchResponse
		if err := json.Unmarshal(out, &resp); err != nil {
			return fmt.Errorf("parsing search response: %w", err)
		}
		total, err := decode(resp.Results)
		if err != nil {
			return fmt.Errorf("parsing search results: %w", err)
		}
		if onProgress != nil {
			onProgress(total)
		}

		if !resp.HasMore || resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	return nil
}

// GetPageMarkdown fetches a single page's content as Markdown via `ntn pages get`.
func GetPageMarkdown(pageID string) (string, error) {
	out, err := runNtn("pages", "get", pageID)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// EditPageInEditor runs `ntn pages edit <id>` with stdio inherited so the user's
// $EDITOR/$VISUAL takes over the terminal. Meant to be run via tea.ExecProcess.
func EditPageInEditor(pageID string) *exec.Cmd {
	cmd := exec.Command("ntn", "pages", "edit", pageID)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func runNtn(args ...string) ([]byte, error) {
	cmd := exec.Command("ntn", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ntn %v: %w: %s", args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}
