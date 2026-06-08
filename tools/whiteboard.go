package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/skridlevsky/graphthulhu/backend"
	"github.com/skridlevsky/graphthulhu/parser"
	"github.com/skridlevsky/graphthulhu/types"
)

// Whiteboard implements whiteboard MCP tools.
type Whiteboard struct {
	client backend.Backend
}

// NewWhiteboard creates a new Whiteboard tool handler.
func NewWhiteboard(c backend.Backend) *Whiteboard {
	return &Whiteboard{client: c}
}

// ListWhiteboards returns all whiteboards in the graph.
func (w *Whiteboard) ListWhiteboards(ctx context.Context, req *mcp.CallToolRequest, input types.ListWhiteboardsInput) (*mcp.CallToolResult, any, error) {
	provider, ok := w.client.(backend.WhiteboardProvider)
	if !ok {
		return errorResult("backend does not support whiteboards"), nil, nil
	}

	entries, err := provider.GetWhiteboards(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to list whiteboards: %v", err)), nil, nil
	}

	if len(entries) == 0 {
		return textResult("No whiteboards found in the graph."), nil, nil
	}

	boards := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		boards = append(boards, map[string]any{
			"uuid":      e.UUID,
			"name":      e.Name,
			"updatedAt": e.UpdatedAt,
		})
	}

	res, err := jsonTextResult(map[string]any{
		"count":       len(boards),
		"whiteboards": boards,
	})
	return res, nil, err
}

// GetWhiteboard retrieves a whiteboard's content including embedded pages and connections.
func (w *Whiteboard) GetWhiteboard(ctx context.Context, req *mcp.CallToolRequest, input types.GetWhiteboardInput) (*mcp.CallToolResult, any, error) {
	// Get the whiteboard page's block tree
	blocks, err := w.client.GetPageBlocksTree(ctx, input.Name)
	if err != nil {
		return errorResult(fmt.Sprintf("whiteboard not found: %s — %v", input.Name, err)), nil, nil
	}

	var elements []map[string]any
	var embeddedPages []string
	var connections []map[string]any
	seenPages := make(map[string]bool)

	for _, b := range blocks {
		element := map[string]any{
			"uuid":    b.UUID,
			"content": b.Content,
		}

		if b.Properties != nil {
			element["properties"] = b.Properties

			// Detect shape type
			if lsType, ok := b.Properties["ls-type"]; ok {
				element["shapeType"] = lsType
			}

			// Detect embedded page references
			if pageRef, ok := b.Properties["logseq.tldraw.page"].(string); ok {
				element["embeddedPage"] = pageRef
				if !seenPages[pageRef] {
					embeddedPages = append(embeddedPages, pageRef)
					seenPages[pageRef] = true
				}
			}

			// Detect connectors (lines between shapes)
			source, hasSource := b.Properties["logseq.tldraw.source"]
			target, hasTarget := b.Properties["logseq.tldraw.target"]
			if hasSource && hasTarget {
				connections = append(connections, map[string]any{
					"source": source,
					"target": target,
				})
			}
		}

		// Also extract links from content
		parsed := parser.Parse(b.Content)
		if len(parsed.Links) > 0 {
			element["links"] = parsed.Links
			for _, link := range parsed.Links {
				if !seenPages[link] {
					embeddedPages = append(embeddedPages, link)
					seenPages[link] = true
				}
			}
		}

		elements = append(elements, element)
	}

	res, err := jsonTextResult(map[string]any{
		"name":          input.Name,
		"elementCount":  len(elements),
		"elements":      elements,
		"embeddedPages": embeddedPages,
		"connections":   connections,
	})
	return res, nil, err
}

