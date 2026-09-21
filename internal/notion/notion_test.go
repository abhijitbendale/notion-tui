package notion

import (
	"encoding/json"
	"testing"
)

func TestPageTitleUsesNamedTitleProperty(t *testing.T) {
	var page Page
	err := json.Unmarshal([]byte(`{
		"id": "page-1",
		"properties": {
			"Company Name": {
				"id": "title",
				"type": "title",
				"title": [{"plain_text": "Pika Labs"}]
			}
		}
	}`), &page)
	if err != nil {
		t.Fatal(err)
	}
	if got := page.Title(); got != "Pika Labs" {
		t.Fatalf("expected named title property, got %q", got)
	}
}

func TestNormalizeDataSourceRowsPutsTitleFirst(t *testing.T) {
	number := 42.5
	table := normalizeDataSourceRows([]DataSourceRow{
		{
			Properties: Properties{
				"Status":       {Type: "select", Select: &SelectValue{Name: "Active"}},
				"Company Name": {Type: "title", Title: []RichText{{PlainText: "Pika Labs"}}},
				"Score":        {Type: "number", Number: &number},
			},
		},
	})

	if len(table.Columns) != 3 || table.Columns[0] != "Company Name" {
		t.Fatalf("expected title column first, got %#v", table.Columns)
	}
	if len(table.Rows) != 1 || table.Rows[0][0] != "Pika Labs" {
		t.Fatalf("unexpected normalized row: %#v", table.Rows[0])
	}
	values := map[string]string{}
	for i, column := range table.Columns {
		values[column] = table.Rows[0][i]
	}
	if values["Status"] != "Active" || values["Score"] != "42.5" {
		t.Fatalf("unexpected normalized values: %#v", values)
	}
}
