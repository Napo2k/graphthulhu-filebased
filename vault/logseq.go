package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/skridlevsky/graphthulhu/types"
)

// logseqFormat implements format for an offline Logseq graph read straight from
// disk. Logseq stores an outliner: every block is a `- ` bullet, nesting is
// expressed with leading tabs, block/page properties use `key:: value`, and
// stable block IDs live in an `id::` property. Pages live under pages/ and
// daily notes under journals/. This is the Logseq counterpart to obsidianFormat.
type logseqFormat struct {
	// journalLayout is the Go time layout that matches the vault's
	// :journal/file-name-format (default "2006_01_02").
	journalLayout string
}

var _ format = (*logseqFormat)(nil)

// newLogseqFormat builds a logseqFormat, reading logseq/config.edn under
// vaultPath for the journal file-name format. Missing or unparseable config
// falls back to Logseq's default.
func newLogseqFormat(vaultPath string) *logseqFormat {
	f := &logseqFormat{journalLayout: "2006_01_02"}
	if layout := readJournalLayout(vaultPath); layout != "" {
		f.journalLayout = layout
	}
	return f
}

var (
	// key:: value — a Logseq property line (matched against a trimmed line).
	logseqPropPattern = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_/-]*)::[ \t]?(.*)$`)
	// a bare UUID, used to validate id:: values.
	logseqUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// Parse converts a Logseq markdown file into a cachedPage.
func (f *logseqFormat) Parse(relPath, content string, info os.FileInfo) *cachedPage {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")

	name, isJournal, journalDay := f.pageIdentity(filepath.ToSlash(relPath))
	lowerName := strings.ToLower(name)

	pageProps, pageOrder, blockStart := splitPageProperties(lines)

	entity := types.PageEntity{
		Name:         name,
		OriginalName: name,
		Journal:      isJournal,
		JournalDay:   journalDay,
		CreatedAt:    info.ModTime().UnixMilli(),
		UpdatedAt:    info.ModTime().UnixMilli(),
	}
	if len(pageProps) > 0 {
		entity.Properties = pageProps
		entity.PropertiesOrder = pageOrder
	}

	blocks := parseLogseqBlocks(relPath, lines[blockStart:], blockStart)

	return &cachedPage{
		entity:    entity,
		lowerName: lowerName,
		filePath:  relPath,
		blocks:    blocks,
	}
}

// pageIdentity derives the page name (and journal metadata) from a vault-relative
// slash path. journals/ files become ISO-dated journal pages; pages/ files have
// their `___` namespace separators decoded back to `/`.
func (f *logseqFormat) pageIdentity(slashPath string) (name string, isJournal bool, journalDay int) {
	stem := strings.TrimSuffix(slashPath, ".md")
	switch {
	case strings.HasPrefix(stem, "journals/"):
		base := stem[len("journals/"):]
		if t, err := time.Parse(f.journalLayout, base); err == nil {
			day := t.Year()*10000 + int(t.Month())*100 + t.Day()
			return t.Format("2006-01-02"), true, day
		}
		// Unparseable journal filename: keep the raw base, still a journal.
		return base, true, 0
	case strings.HasPrefix(stem, "pages/"):
		return decodeNamespace(stem[len("pages/"):]), false, 0
	default:
		return decodeNamespace(stem), false, 0
	}
}

// PageFilePath maps a page name to its file under pages/, encoding namespace
// separators as `___` (Logseq's triple-underscore convention).
func (f *logseqFormat) PageFilePath(name string) string {
	return "pages/" + strings.ReplaceAll(name, "/", "___") + ".md"
}

// NewPageContent renders page properties as a leading pre-block of bare
// `key:: value` lines. Keys are sorted for deterministic output.
func (f *logseqFormat) NewPageContent(properties map[string]any) string {
	if len(properties) == 0 {
		return ""
	}
	keys := make([]string, 0, len(properties))
	for k := range properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s:: %v\n", k, properties[k])
	}
	return b.String()
}

// EmbedID appends an `id::` property line carrying the block UUID.
func (f *logseqFormat) EmbedID(content, uuid string) string {
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return "id:: " + uuid
	}
	return content + "\nid:: " + uuid
}

// ExtractID pulls an `id::` UUID out of content (if present) and returns the
// content with that line removed.
func (f *logseqFormat) ExtractID(content string) (string, string) {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	uuid := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := logseqPropPattern.FindStringSubmatch(trimmed); m != nil && m[1] == "id" {
			if v := strings.TrimSpace(m[2]); logseqUUIDPattern.MatchString(v) {
				uuid = v
				continue
			}
		}
		kept = append(kept, line)
	}
	return uuid, strings.TrimRight(strings.Join(kept, "\n"), "\n")
}

// RenderBlock serializes a brand-new top-level block as a `- ` bullet with an
// indented `id::` property line.
func (f *logseqFormat) RenderBlock(content, uuid string) string {
	return renderLogseqBlock(content, uuid, 0)
}

// RenderChild serializes childContent as a child of the parent whose first
// on-disk line is parentLine, indented one tab deeper than the parent. The
// result includes the leading newline so it splices in after the parent block.
func (f *logseqFormat) RenderChild(parentLine, childContent, uuid string) string {
	depth := 0
	for depth < len(parentLine) && parentLine[depth] == '\t' {
		depth++
	}
	return "\n" + renderLogseqBlock(childContent, uuid, depth+1)
}

// renderLogseqBlock renders content as an outliner block at the given tab depth.
// The first line gets the `- ` bullet; continuation lines and the `id::`
// property align two spaces under the bullet text (matching Logseq's layout).
func renderLogseqBlock(content, uuid string, depth int) string {
	tab := strings.Repeat("\t", depth)
	cont := tab + "  "
	var b strings.Builder
	if content == "" {
		b.WriteString(tab + "-")
	} else {
		lines := strings.Split(content, "\n")
		b.WriteString(tab + "- " + lines[0])
		for _, l := range lines[1:] {
			b.WriteString("\n" + cont + l)
		}
	}
	if uuid != "" {
		b.WriteString("\n" + cont + "id:: " + uuid)
	}
	return b.String()
}

// SplitLeadingProperties peels off a file's leading page-property pre-block
// (bare `key:: value` lines before the first bullet) so a prepended block lands
// after the page properties. head retains a trailing newline; body is the rest.
func (f *logseqFormat) SplitLeadingProperties(content string) (head, body string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	end := 0 // exclusive index past the last consumed pre-block property line
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue // blank lines may interleave the pre-block
		}
		if _, isBullet, _ := bulletInfo(line); isBullet {
			break
		}
		if logseqPropPattern.MatchString(trimmed) {
			end = i + 1
			continue
		}
		break // freeform text: not a page-property pre-block
	}
	if end == 0 {
		return "", content
	}
	return strings.Join(lines[:end], "\n") + "\n", strings.Join(lines[end:], "\n")
}

// decodeNamespace turns a `___`-encoded filename stem into a `/` page name.
func decodeNamespace(s string) string {
	return strings.ReplaceAll(s, "___", "/")
}

// bulletInfo reports a line's tab depth, whether it opens a `- ` block, and the
// block text following the bullet.
func bulletInfo(line string) (depth int, isBullet bool, rest string) {
	i := 0
	for i < len(line) && line[i] == '\t' {
		i++
	}
	s := line[i:]
	switch {
	case s == "-":
		return i, true, ""
	case strings.HasPrefix(s, "- "):
		return i, true, s[2:]
	default:
		return i, false, ""
	}
}

// splitPageProperties consumes leading bare `key:: value` lines (the pre-block
// that holds page properties) and returns them plus the index of the first
// block line. Blank lines among the pre-block are skipped.
func splitPageProperties(lines []string) (props map[string]any, order []string, blockStart int) {
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if _, isBullet, _ := bulletInfo(line); isBullet {
			return props, order, i
		}
		m := logseqPropPattern.FindStringSubmatch(trimmed)
		if m == nil {
			// Freeform leading text (not a Logseq pre-block): blocks start here.
			return props, order, i
		}
		if props == nil {
			props = make(map[string]any)
		}
		key := m[1]
		if _, exists := props[key]; !exists {
			order = append(order, key)
		}
		props[key] = strings.TrimSpace(m[2])
	}
	return props, order, len(lines)
}

// lsNode is a mutable parse-tree node. A pointer tree is built first, then
// converted to []types.BlockEntity, so accumulating continuation lines never
// races slice reallocation.
type lsNode struct {
	depth    int
	lineNo   int // absolute file line index, for deterministic UUID fallback
	rawLines []string
	children []*lsNode
}

// parseLogseqBlocks builds a block tree from the outliner lines. lineOffset is
// the absolute index of lines[0] within the file (for UUID seeding).
func parseLogseqBlocks(relPath string, lines []string, lineOffset int) []types.BlockEntity {
	var roots []*lsNode
	var stack []*lsNode

	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		abs := lineOffset + i
		depth, isBullet, rest := bulletInfo(line)

		if isBullet {
			node := &lsNode{depth: depth, lineNo: abs, rawLines: []string{rest}}
			for len(stack) > 0 && stack[len(stack)-1].depth >= depth {
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 {
				roots = append(roots, node)
			} else {
				top := stack[len(stack)-1]
				top.children = append(top.children, node)
			}
			stack = append(stack, node)
			continue
		}

		// Continuation / property line of the current block.
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(stack) == 0 {
			// Freeform text with no bullet: start an implicit root block.
			node := &lsNode{depth: 0, lineNo: abs}
			roots = append(roots, node)
			stack = append(stack, node)
		}
		top := stack[len(stack)-1]
		top.rawLines = append(top.rawLines, deindentContinuation(line, top.depth))
	}

	out := make([]types.BlockEntity, 0, len(roots))
	for _, n := range roots {
		out = append(out, finalizeNode(relPath, n))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// deindentContinuation strips a continuation line's alignment: up to depth tabs
// followed by up to two alignment spaces (Logseq aligns wrapped lines/properties
// under the bullet text).
func deindentContinuation(line string, depth int) string {
	i := 0
	for i < len(line) && i < depth && line[i] == '\t' {
		i++
	}
	rest := line[i:]
	for s := 0; s < 2 && strings.HasPrefix(rest, " "); s++ {
		rest = rest[1:]
	}
	return rest
}

// finalizeNode converts a parse node into a BlockEntity, splitting out the
// id:: UUID and other block properties.
func finalizeNode(relPath string, n *lsNode) types.BlockEntity {
	contentLines := make([]string, 0, len(n.rawLines))
	var props map[string]any
	var order []string
	uuid := ""

	for _, raw := range n.rawLines {
		trimmed := strings.TrimSpace(raw)
		if m := logseqPropPattern.FindStringSubmatch(trimmed); m != nil {
			key := m[1]
			val := strings.TrimSpace(m[2])
			if key == "id" && logseqUUIDPattern.MatchString(val) {
				uuid = val
				continue // id:: is metadata, not content
			}
			if props == nil {
				props = make(map[string]any)
			}
			if _, ok := props[key]; !ok {
				order = append(order, key)
			}
			props[key] = val
			contentLines = append(contentLines, raw) // properties stay in content
			continue
		}
		contentLines = append(contentLines, raw)
	}

	if uuid == "" {
		uuid = deterministicUUID(relPath, n.lineNo)
	}

	block := types.BlockEntity{
		UUID:    uuid,
		Content: strings.TrimRight(strings.Join(contentLines, "\n"), "\n "),
	}
	if len(props) > 0 {
		block.Properties = props
		block.PropertiesOrder = order
	}
	for _, c := range n.children {
		block.Children = append(block.Children, finalizeNode(relPath, c))
	}
	return block
}

// journalFormatPattern extracts :journal/file-name-format from config.edn.
var journalFormatPattern = regexp.MustCompile(`:journal/file-name-format\s+"([^"]+)"`)

// readJournalLayout reads logseq/config.edn under vaultPath and returns a Go
// time layout for the journal file-name format, or "" if unavailable.
func readJournalLayout(vaultPath string) string {
	data, err := os.ReadFile(filepath.Join(vaultPath, "logseq", "config.edn"))
	if err != nil {
		return ""
	}
	m := journalFormatPattern.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return logseqDateLayout(string(m[1]))
}

// logseqDateLayout translates common Logseq date-format tokens into a Go
// reference-time layout. Longer tokens are replaced first to avoid partial hits.
func logseqDateLayout(format string) string {
	repl := strings.NewReplacer(
		"yyyy", "2006",
		"MMMM", "January",
		"MMM", "Jan",
		"dd", "02",
		"MM", "01",
		"EEEE", "Monday",
	)
	return repl.Replace(format)
}
