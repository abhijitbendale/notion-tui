// Package tree builds a hierarchical page tree from flat Notion search results.
package tree

import (
	"sort"
	"strings"

	"notion-tui/internal/notion"
)

// Node is one entry in the tree: either a real page, or a synthetic container
// node standing in for a database whose rows (pages) it groups together.
type Node struct {
	ID          string
	title       string
	url         string
	lastEdited  string
	IsContainer bool // true if this node represents a database, not a real page
	Children    []*Node
}

// Title returns the node's display title.
func (n *Node) Title() string { return n.title }

// URL returns the node's Notion URL (empty for container nodes without one).
func (n *Node) URL() string { return n.url }

// LastEdited returns the node's last_edited_time, used as a cache key for pages.
func (n *Node) LastEdited() string { return n.lastEdited }

// Build turns flat pages and data sources into a forest of trees.
//
// Notion pages living inside a database have parent.type == "data_source_id"
// rather than a page_id, and the data source itself doesn't carry a page_id
// parent either — instead it carries a "database_parent" pointing at the page
// (or workspace) the database is anchored to. We use that to insert a
// synthetic container node for the database, so rows nest under their
// database, which nests under its anchor page, matching the Notion sidebar.
func Build(pages []notion.Page, dataSources []notion.DataSource) []*Node {
	nodes := make(map[string]*Node, len(pages)+len(dataSources))

	for _, p := range pages {
		nodes[p.ID] = &Node{ID: p.ID, title: p.Title(), url: p.URL, lastEdited: p.LastEdited}
	}
	for _, d := range dataSources {
		nodes[d.ID] = &Node{ID: d.ID, title: d.Title(), url: d.URL, IsContainer: true}
	}

	// dsParent maps a data source ID to the id it should be attached under
	// (its database_parent page, or "" if anchored at the workspace root).
	dsParent := make(map[string]string, len(dataSources))
	for _, d := range dataSources {
		if d.DatabaseParent.Type == "page_id" {
			dsParent[d.ID] = d.DatabaseParent.PageID
		}
	}

	var roots []*Node
	attach := func(id, parentID string) {
		n := nodes[id]
		if parent, ok := nodes[parentID]; ok && parentID != "" {
			parent.Children = append(parent.Children, n)
		} else {
			roots = append(roots, n)
		}
	}

	for _, d := range dataSources {
		attach(d.ID, dsParent[d.ID])
	}
	for _, p := range pages {
		switch p.Parent.Type {
		case "page_id":
			attach(p.ID, p.Parent.PageID)
		case "data_source_id":
			attach(p.ID, p.Parent.DataSourceID)
		default:
			attach(p.ID, "")
		}
	}

	sortTree(roots)
	return roots
}

func sortTree(nodes []*Node) {
	sort.Slice(nodes, func(i, j int) bool {
		return strings.ToLower(nodes[i].Title()) < strings.ToLower(nodes[j].Title())
	})
	for _, n := range nodes {
		sortTree(n.Children)
	}
}
