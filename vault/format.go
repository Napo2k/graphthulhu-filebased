package vault

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/skridlevsky/graphthulhu/types"
)

// format encapsulates the on-disk syntax of a knowledge graph: how file bytes
// parse into a block tree, where a page's file lives, and how block IDs and
// page properties are serialized. Client owns all format-agnostic machinery
// (indexing, watching, search, backlinks) and delegates every format-specific
// decision to its format. This is the seam that lets a single Client serve
// both an Obsidian vault and an offline Logseq graph.
type format interface {
	// Parse converts a single file's bytes into a cachedPage: block tree,
	// page properties, and journal flag. relPath is vault-relative; info
	// supplies timestamps.
	Parse(relPath, content string, info os.FileInfo) *cachedPage

	// PageFilePath returns the vault-relative file path for a page name.
	PageFilePath(name string) string

	// NewPageContent renders the initial file contents for a new page created
	// with the given properties (empty string when there is nothing to write).
	NewPageContent(properties map[string]any) string

	// EmbedID returns content with the block UUID embedded for persistence.
	EmbedID(content, uuid string) string

	// ExtractID returns an embedded UUID (or "" when absent) and the content
	// with the ID marker stripped.
	ExtractID(content string) (uuid, clean string)

	// RenderBlock serializes a brand-new top-level block (its content plus
	// uuid) into the on-disk text for a standalone block — no trailing
	// newline. Obsidian emits the content with an embedded id comment; Logseq
	// emits a `- ` bullet with an indented `id::` property line.
	RenderBlock(content, uuid string) string

	// RenderChild serializes childContent (with uuid) as a child of the block
	// whose first on-disk line is parentLine, returning the text to splice in
	// immediately after the parent block — including the leading newline.
	// parentLine carries the parent's indentation/heading context so the
	// format can nest correctly (Obsidian: heading level + 1; Logseq: parent
	// tab depth + 1).
	RenderChild(parentLine, childContent, uuid string) string

	// SplitLeadingProperties separates a file's leading page-property pre-block
	// (Obsidian YAML frontmatter; Logseq leading `key:: value` lines) from the
	// rest of the body, so a prepended block lands after the page properties.
	SplitLeadingProperties(content string) (head, body string)

	// ChildInsertOffset returns the byte offset in fileStr at which a new child
	// of the parent block should be spliced. contentEnd is the offset just past
	// the parent's matched block.Content; parentLine is the parent's first
	// on-disk line (for indentation context). Obsidian inserts at contentEnd —
	// its block content spans the whole heading section, id comment included.
	// Logseq advances past the parent's trailing continuation lines (the
	// de-indented `id::`/property lines that are not part of block.Content) so
	// the child lands after them but before any existing children.
	ChildInsertOffset(fileStr string, contentEnd int, parentLine string) int
}

// obsidianFormat implements format for an Obsidian vault: heading-sectioned
// markdown, YAML frontmatter for page properties, and <!-- id: UUID --> HTML
// comments for stable block IDs.
type obsidianFormat struct {
	dailyFolder string // daily-notes subfolder; "" disables journal detection
}

func (f *obsidianFormat) Parse(relPath, content string, info os.FileInfo) *cachedPage {
	name := strings.TrimSuffix(filepath.ToSlash(relPath), ".md")
	lowerName := strings.ToLower(name)

	props, body := parseFrontmatter(content)

	isJournal := false
	if f.dailyFolder != "" {
		prefix := strings.ToLower(f.dailyFolder) + "/"
		isJournal = strings.HasPrefix(lowerName, prefix)
	}

	entity := types.PageEntity{
		Name:         name,
		OriginalName: name,
		Properties:   props,
		Journal:      isJournal,
		CreatedAt:    info.ModTime().UnixMilli(),
		UpdatedAt:    info.ModTime().UnixMilli(),
	}

	blocks := parseMarkdownBlocks(relPath, body)

	return &cachedPage{
		entity:    entity,
		lowerName: lowerName,
		filePath:  relPath,
		blocks:    blocks,
	}
}

func (f *obsidianFormat) PageFilePath(name string) string {
	return name + ".md"
}

func (f *obsidianFormat) NewPageContent(properties map[string]any) string {
	return renderFrontmatter(properties)
}

func (f *obsidianFormat) EmbedID(content, uuid string) string {
	return embedUUID(content, uuid)
}

func (f *obsidianFormat) ExtractID(content string) (string, string) {
	return extractUUID(content)
}

func (f *obsidianFormat) RenderBlock(content, uuid string) string {
	return embedUUID(content, uuid)
}

func (f *obsidianFormat) RenderChild(parentLine, childContent, uuid string) string {
	rendered := childContent
	if lvl := headingLevel(strings.SplitN(parentLine, "\n", 2)[0]); lvl > 0 && lvl < 6 {
		if headingLevel(strings.SplitN(childContent, "\n", 2)[0]) == 0 {
			rendered = strings.Repeat("#", lvl+1) + " " + childContent
		}
	}
	return "\n" + embedUUID(rendered, uuid)
}

func (f *obsidianFormat) SplitLeadingProperties(content string) (head, body string) {
	props, body := parseFrontmatter(content)
	if props != nil {
		return renderFrontmatter(props), body
	}
	return "", content
}

// ChildInsertOffset for Obsidian is contentEnd unchanged: a block's content
// spans the whole heading section (with its embedded id comment), so the child
// heading is spliced right after it.
func (f *obsidianFormat) ChildInsertOffset(_ string, contentEnd int, _ string) int {
	return contentEnd
}
