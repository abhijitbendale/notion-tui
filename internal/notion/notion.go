// Package notion wraps the `ntn` CLI as subprocess calls.
package notion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
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

// Properties contains Notion properties keyed by their user-defined names.
type Properties map[string]Property

// Property contains the common value shapes needed for read-only database views.
type Property struct {
	Type        string          `json:"type"`
	Title       []RichText      `json:"title"`
	RichText    []RichText      `json:"rich_text"`
	Number      *float64        `json:"number"`
	URL         *string         `json:"url"`
	Email       *string         `json:"email"`
	PhoneNumber *string         `json:"phone_number"`
	Checkbox    bool            `json:"checkbox"`
	Select      *SelectValue    `json:"select"`
	Status      *SelectValue    `json:"status"`
	MultiSelect []SelectValue   `json:"multi_select"`
	Date        *DateValue      `json:"date"`
	People      []PersonValue   `json:"people"`
	Relation    []RelationValue `json:"relation"`
	Formula     json.RawMessage `json:"formula"`
	Rollup      json.RawMessage `json:"rollup"`
	CreatedTime string          `json:"created_time"`
	LastEdited  string          `json:"last_edited_time"`
}

type SelectValue struct {
	Name string `json:"name"`
}

type DateValue struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type PersonValue struct {
	Name string `json:"name"`
}

type RelationValue struct {
	ID string `json:"id"`
}

// RichText is a single rich-text fragment.
type RichText struct {
	PlainText string `json:"plain_text"`
}

// Title returns the page's plain-text title, or "Untitled" if empty.
func (p Page) Title() string {
	for _, property := range p.Properties {
		if property.Type == "title" {
			return richTextTitle(property.Title)
		}
	}
	return "Untitled"
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

// DataSourceRow is one database row returned by `ntn datasources query`.
type DataSourceRow struct {
	ID         string     `json:"id"`
	Properties Properties `json:"properties"`
}

// DataSourceTable is a normalized, display-ready database result.
type DataSourceTable struct {
	Columns []string
	Rows    [][]string
}

type dataSourceQueryResponse struct {
	Results    []DataSourceRow `json:"results"`
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

// QueryDataSource fetches all visible rows from a data source for read-only display.
func QueryDataSource(id string) (DataSourceTable, error) {
	var rows []DataSourceRow
	cursor := ""
	for {
		args := []string{"datasources", "query", id, "--limit", "100", "--json"}
		if cursor != "" {
			args = append(args, "--start-cursor", cursor)
		}
		out, err := runNtn(args...)
		if err != nil {
			return DataSourceTable{}, err
		}
		var response dataSourceQueryResponse
		if err := json.Unmarshal(out, &response); err != nil {
			return DataSourceTable{}, fmt.Errorf("parsing data source response: %w", err)
		}
		rows = append(rows, response.Results...)
		if !response.HasMore || response.NextCursor == "" {
			break
		}
		cursor = response.NextCursor
	}
	return normalizeDataSourceRows(rows), nil
}

func normalizeDataSourceRows(rows []DataSourceRow) DataSourceTable {
	columnSet := map[string]bool{}
	for _, row := range rows {
		for name := range row.Properties {
			columnSet[name] = true
		}
	}
	columns := make([]string, 0, len(columnSet))
	for name := range columnSet {
		columns = append(columns, name)
	}
	sort.SliceStable(columns, func(i, j int) bool {
		return strings.ToLower(columns[i]) < strings.ToLower(columns[j])
	})
	for i, name := range columns {
		for _, row := range rows {
			if property, ok := row.Properties[name]; ok && property.Type == "title" {
				columns = append([]string{name}, append(columns[:i], columns[i+1:]...)...)
				break
			}
		}
		if columns[0] == name {
			break
		}
	}

	table := DataSourceTable{Columns: columns, Rows: make([][]string, 0, len(rows))}
	for _, row := range rows {
		values := make([]string, len(columns))
		for i, column := range columns {
			values[i] = propertyValue(row.Properties[column])
		}
		table.Rows = append(table.Rows, values)
	}
	return table
}

func propertyValue(property Property) string {
	switch property.Type {
	case "title":
		return richTextTitle(property.Title)
	case "rich_text":
		return richTextTitle(property.RichText)
	case "number":
		if property.Number == nil {
			return ""
		}
		return fmt.Sprintf("%g", *property.Number)
	case "url":
		return pointerString(property.URL)
	case "email":
		return pointerString(property.Email)
	case "phone_number":
		return pointerString(property.PhoneNumber)
	case "checkbox":
		if property.Checkbox {
			return "Yes"
		}
		return "No"
	case "select":
		if property.Select != nil {
			return property.Select.Name
		}
	case "status":
		if property.Status != nil {
			return property.Status.Name
		}
	case "multi_select":
		return joinSelectValues(property.MultiSelect)
	case "date":
		if property.Date != nil {
			return property.Date.Start
		}
	case "people":
		values := make([]string, 0, len(property.People))
		for _, person := range property.People {
			values = append(values, person.Name)
		}
		return strings.Join(values, ", ")
	case "relation":
		values := make([]string, 0, len(property.Relation))
		for _, relation := range property.Relation {
			values = append(values, relation.ID)
		}
		return strings.Join(values, ", ")
	case "created_time":
		return property.CreatedTime
	case "last_edited_time":
		return property.LastEdited
	case "formula", "rollup":
		if property.Type == "rollup" {
			return compactJSON(property.Rollup)
		}
		return compactJSON(property.Formula)
	}
	return ""
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func joinSelectValues(values []SelectValue) string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.Name)
	}
	return strings.Join(names, ", ")
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
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
