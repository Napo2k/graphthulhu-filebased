package vault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skridlevsky/graphthulhu/types"
)

// fakeFileInfo is a minimal os.FileInfo for exercising Parse without disk I/O.
type fakeFileInfo struct{ mod time.Time }

func (f fakeFileInfo) Name() string       { return "test.md" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0o644 }
func (f fakeFileInfo) ModTime() time.Time { return f.mod }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

func TestBulletInfo(t *testing.T) {
	tests := []struct {
		line     string
		depth    int
		isBullet bool
		rest     string
	}{
		{"- top", 0, true, "top"},
		{"\t- child", 1, true, "child"},
		{"\t\t- deep", 2, true, "deep"},
		{"-", 0, true, ""},
		{"  not a bullet", 0, false, ""},
		{"\tkey:: value", 1, false, ""},
		{"", 0, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			d, b, r := bulletInfo(tt.line)
			if d != tt.depth || b != tt.isBullet || r != tt.rest {
				t.Errorf("bulletInfo(%q) = (%d,%v,%q), want (%d,%v,%q)",
					tt.line, d, b, r, tt.depth, tt.isBullet, tt.rest)
			}
		})
	}
}

func TestSplitPageProperties(t *testing.T) {
	t.Run("pre-block properties", func(t *testing.T) {
		lines := strings.Split("title:: Foo\ntags:: bar, baz\n\n- First block", "\n")
		props, order, start := splitPageProperties(lines)
		if props["title"] != "Foo" || props["tags"] != "bar, baz" {
			t.Errorf("props = %v", props)
		}
		if len(order) != 2 || order[0] != "title" || order[1] != "tags" {
			t.Errorf("order = %v", order)
		}
		if lines[start] != "- First block" {
			t.Errorf("blockStart points at %q", lines[start])
		}
	})

	t.Run("no properties, starts with bullet", func(t *testing.T) {
		lines := strings.Split("- only blocks here", "\n")
		props, _, start := splitPageProperties(lines)
		if props != nil {
			t.Errorf("expected nil props, got %v", props)
		}
		if start != 0 {
			t.Errorf("blockStart = %d, want 0", start)
		}
	})
}

func TestParseLogseqBlocksNesting(t *testing.T) {
	body := "- Parent\n\t- Child\n\t\t- Grandchild\n\t- Sibling\n- Second root"
	blocks := parseLogseqBlocks("test.md", strings.Split(body, "\n"), 0)

	if len(blocks) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(blocks))
	}
	if blocks[0].Content != "Parent" {
		t.Errorf("root[0] = %q", blocks[0].Content)
	}
	if len(blocks[0].Children) != 2 {
		t.Fatalf("Parent should have 2 children, got %d", len(blocks[0].Children))
	}
	if blocks[0].Children[0].Content != "Child" {
		t.Errorf("child[0] = %q", blocks[0].Children[0].Content)
	}
	if len(blocks[0].Children[0].Children) != 1 || blocks[0].Children[0].Children[0].Content != "Grandchild" {
		t.Errorf("grandchild missing: %+v", blocks[0].Children[0].Children)
	}
	if blocks[0].Children[1].Content != "Sibling" {
		t.Errorf("child[1] = %q", blocks[0].Children[1].Content)
	}
	if blocks[1].Content != "Second root" {
		t.Errorf("root[1] = %q", blocks[1].Content)
	}
}

func TestParseLogseqBlockIDAndProperties(t *testing.T) {
	id := "6543abcd-1234-5678-9abc-def012345678"
	body := "- Do the thing #task\n  priority:: A\n  id:: " + id
	blocks := parseLogseqBlocks("test.md", strings.Split(body, "\n"), 0)

	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	b := blocks[0]
	if b.UUID != id {
		t.Errorf("UUID = %q, want %q", b.UUID, id)
	}
	if strings.Contains(b.Content, "id::") {
		t.Errorf("id:: should be stripped from content: %q", b.Content)
	}
	if b.Content != "Do the thing #task\npriority:: A" {
		t.Errorf("content = %q", b.Content)
	}
	if b.Properties["priority"] != "A" {
		t.Errorf("properties = %v", b.Properties)
	}
}

func TestParseLogseqBlockDeterministicUUID(t *testing.T) {
	body := "- no id here"
	a := parseLogseqBlocks("test.md", strings.Split(body, "\n"), 0)
	b := parseLogseqBlocks("test.md", strings.Split(body, "\n"), 0)
	if a[0].UUID != b[0].UUID {
		t.Errorf("deterministic UUID not stable: %q != %q", a[0].UUID, b[0].UUID)
	}
	if len(a[0].UUID) != 36 {
		t.Errorf("UUID length = %d, want 36", len(a[0].UUID))
	}
}

func TestLogseqFormatParse(t *testing.T) {
	f := &logseqFormat{journalLayout: "2006_01_02"}
	info := fakeFileInfo{mod: time.Unix(1700000000, 0)}

	t.Run("page with properties", func(t *testing.T) {
		content := "type:: project\n\n- First block\n- Second block"
		page := f.Parse("pages/Graphthulhu.md", content, info)
		if page.entity.Name != "Graphthulhu" {
			t.Errorf("name = %q", page.entity.Name)
		}
		if page.entity.Properties["type"] != "project" {
			t.Errorf("page props = %v", page.entity.Properties)
		}
		if len(page.blocks) != 2 {
			t.Fatalf("expected 2 blocks, got %d", len(page.blocks))
		}
	})

	t.Run("namespace decoding", func(t *testing.T) {
		page := f.Parse("pages/projects___graphthulhu.md", "- x", info)
		if page.entity.Name != "projects/graphthulhu" {
			t.Errorf("name = %q, want projects/graphthulhu", page.entity.Name)
		}
	})

	t.Run("journal detection", func(t *testing.T) {
		page := f.Parse("journals/2024_01_15.md", "- woke up", info)
		if !page.entity.Journal {
			t.Error("expected Journal = true")
		}
		if page.entity.Name != "2024-01-15" {
			t.Errorf("journal name = %q, want 2024-01-15", page.entity.Name)
		}
		if page.entity.JournalDay != 20240115 {
			t.Errorf("JournalDay = %d, want 20240115", page.entity.JournalDay)
		}
	})
}

func TestLogseqPageFilePath(t *testing.T) {
	f := &logseqFormat{}
	if got := f.PageFilePath("Foo"); got != "pages/Foo.md" {
		t.Errorf("PageFilePath(Foo) = %q", got)
	}
	if got := f.PageFilePath("projects/graphthulhu"); got != "pages/projects___graphthulhu.md" {
		t.Errorf("PageFilePath namespace = %q", got)
	}
}

func TestLogseqEmbedExtractID(t *testing.T) {
	f := &logseqFormat{}
	id := "6543abcd-1234-5678-9abc-def012345678"

	embedded := f.EmbedID("Hello world", id)
	if embedded != "Hello world\nid:: "+id {
		t.Errorf("EmbedID = %q", embedded)
	}

	gotID, clean := f.ExtractID(embedded)
	if gotID != id {
		t.Errorf("ExtractID id = %q, want %q", gotID, id)
	}
	if clean != "Hello world" {
		t.Errorf("ExtractID clean = %q", clean)
	}

	// No id present.
	gotID, clean = f.ExtractID("just text")
	if gotID != "" || clean != "just text" {
		t.Errorf("ExtractID(no id) = (%q, %q)", gotID, clean)
	}
}

func TestLogseqNewPageContent(t *testing.T) {
	f := &logseqFormat{}
	if got := f.NewPageContent(nil); got != "" {
		t.Errorf("NewPageContent(nil) = %q", got)
	}
	got := f.NewPageContent(map[string]any{"type": "project", "alias": "gt"})
	// Keys are sorted: alias before type.
	want := "alias:: gt\ntype:: project\n"
	if got != want {
		t.Errorf("NewPageContent = %q, want %q", got, want)
	}
}

func TestLogseqRenderBlock(t *testing.T) {
	f := &logseqFormat{}
	id := "6543abcd-1234-5678-9abc-def012345678"

	t.Run("single line", func(t *testing.T) {
		got := f.RenderBlock("Hello world", id)
		want := "- Hello world\n  id:: " + id
		if got != want {
			t.Errorf("RenderBlock = %q, want %q", got, want)
		}
	})

	t.Run("multi-line aligns continuations", func(t *testing.T) {
		got := f.RenderBlock("First line\nsecond line", id)
		want := "- First line\n  second line\n  id:: " + id
		if got != want {
			t.Errorf("RenderBlock = %q, want %q", got, want)
		}
	})
}

func TestLogseqRenderChild(t *testing.T) {
	f := &logseqFormat{}
	id := "6543abcd-1234-5678-9abc-def012345678"

	t.Run("under a root parent", func(t *testing.T) {
		got := f.RenderChild("- Parent", "Child", id)
		want := "\n\t- Child\n\t  id:: " + id
		if got != want {
			t.Errorf("RenderChild = %q, want %q", got, want)
		}
	})

	t.Run("under an indented parent", func(t *testing.T) {
		got := f.RenderChild("\t\t- Deep parent", "Child", id)
		want := "\n\t\t\t- Child\n\t\t\t  id:: " + id
		if got != want {
			t.Errorf("RenderChild = %q, want %q", got, want)
		}
	})
}

func TestLogseqSplitLeadingProperties(t *testing.T) {
	f := &logseqFormat{}

	t.Run("page properties present", func(t *testing.T) {
		head, body := f.SplitLeadingProperties("title:: Foo\ntype:: project\n\n- First block")
		if head != "title:: Foo\ntype:: project\n" {
			t.Errorf("head = %q", head)
		}
		if body != "\n- First block" {
			t.Errorf("body = %q", body)
		}
	})

	t.Run("no properties", func(t *testing.T) {
		head, body := f.SplitLeadingProperties("- only blocks")
		if head != "" {
			t.Errorf("head = %q, want empty", head)
		}
		if body != "- only blocks" {
			t.Errorf("body = %q", body)
		}
	})
}

// TestLogseqSerializeRoundTrip verifies a rendered block parses back into the
// same content, UUID, and (for children) nesting — the property Step 8 hardens.
func TestLogseqSerializeRoundTrip(t *testing.T) {
	f := &logseqFormat{journalLayout: "2006_01_02"}
	info := fakeFileInfo{mod: time.Unix(1700000000, 0)}
	id := "6543abcd-1234-5678-9abc-def012345678"
	childID := "abcd6543-5678-1234-9abc-def012345678"

	root := f.RenderBlock("Parent block", id)
	child := f.RenderChild("- Parent block", "Child block", childID)
	file := root + child

	page := f.Parse("pages/Round.md", file, info)
	if len(page.blocks) != 1 {
		t.Fatalf("expected 1 root, got %d", len(page.blocks))
	}
	if page.blocks[0].Content != "Parent block" {
		t.Errorf("root content = %q", page.blocks[0].Content)
	}
	if page.blocks[0].UUID != id {
		t.Errorf("root UUID = %q, want %q", page.blocks[0].UUID, id)
	}
	if len(page.blocks[0].Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(page.blocks[0].Children))
	}
	if page.blocks[0].Children[0].Content != "Child block" {
		t.Errorf("child content = %q", page.blocks[0].Children[0].Content)
	}
	if page.blocks[0].Children[0].UUID != childID {
		t.Errorf("child UUID = %q, want %q", page.blocks[0].Children[0].UUID, childID)
	}
}

func TestLogseqGetBlockReferences(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Target block carries a stable id::; two other blocks reference it via
	// ((uuid)); a third block (and the target itself) do not.
	target := "6543abcd-1234-5678-9abc-def012345678"
	notes := "- Target block\n  id:: " + target + "\n- See ((" + target + ")) for details\n- Unrelated block\n"
	other := "- Also relates to ((" + target + "))\n"
	if err := os.WriteFile(filepath.Join(dir, "pages", "Notes.md"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pages", "Other.md"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewLogseq(dir)
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	refs, err := c.GetBlockReferences(context.Background(), target)
	if err != nil {
		t.Fatalf("GetBlockReferences: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 references, got %d: %+v", len(refs), refs)
	}
	// Sorted by page then content: Notes before Other.
	if refs[0].Page != "Notes" || !strings.Contains(refs[0].Content, "See ((") {
		t.Errorf("ref[0] = %+v", refs[0])
	}
	if refs[1].Page != "Other" || !strings.Contains(refs[1].Content, "Also relates") {
		t.Errorf("ref[1] = %+v", refs[1])
	}

	// A UUID nobody references yields no results, not an error.
	none, err := c.GetBlockReferences(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GetBlockReferences(none): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 references, got %d", len(none))
	}
}

func TestLogseqGetFlashcards(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	// One reviewed card (#card + SRS props), one new card ([[card]] ref, no
	// props), and a non-card block that must be excluded.
	notes := "- What is a monad? #card\n  card-repeats:: 3\n  card-next-schedule:: 2030-01-01\n" +
		"- Define functor [[card]]\n" +
		"- Just a regular block\n"
	if err := os.WriteFile(filepath.Join(dir, "pages", "Notes.md"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewLogseq(dir)
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cards, err := c.GetFlashcards(context.Background())
	if err != nil {
		t.Fatalf("GetFlashcards: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d: %+v", len(cards), cards)
	}
	// Sorted by content: "Define functor..." before "What is a monad?...".
	if !strings.Contains(cards[0].Content, "Define functor") {
		t.Errorf("card[0] = %q", cards[0].Content)
	}
	monad := cards[1]
	if !strings.Contains(monad.Content, "What is a monad?") {
		t.Errorf("card[1] = %q", monad.Content)
	}
	if monad.Properties["card-repeats"] != "3" {
		t.Errorf("card-repeats = %v, want \"3\"", monad.Properties["card-repeats"])
	}
	if monad.Properties["card-next-schedule"] != "2030-01-01" {
		t.Errorf("card-next-schedule = %v", monad.Properties["card-next-schedule"])
	}
}

func TestLogseqInsertChildAfterParentProps(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pages", "Deck.md"), []byte("- existing block\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewLogseq(dir)
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx := context.Background()

	// Append a parent block (writes `- front` + an indented `id::` line), then
	// insert a child under it. The child must land after the parent's id:: line.
	parent, err := c.AppendBlockInPage(ctx, "Deck", "What is 2+2? #card")
	if err != nil {
		t.Fatalf("AppendBlockInPage: %v", err)
	}
	if _, err := c.InsertBlock(ctx, parent.UUID, "4", map[string]any{"isPageBlock": false}); err != nil {
		t.Fatalf("InsertBlock: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "pages", "Deck.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	parentIDLine := strings.Index(got, "\n  id:: "+parent.UUID)
	childBullet := strings.Index(got, "\n\t- 4")
	if parentIDLine < 0 {
		t.Fatalf("parent id:: line missing on disk:\n%s", got)
	}
	if childBullet < 0 {
		t.Fatalf("child bullet missing on disk:\n%s", got)
	}
	if parentIDLine > childBullet {
		t.Errorf("parent id:: must precede child bullet, got:\n%s", got)
	}

	// And it must re-parse with the child nested under the parent.
	blocks, err := c.GetPageBlocksTree(ctx, "Deck")
	if err != nil {
		t.Fatalf("GetPageBlocksTree: %v", err)
	}
	var card *types.BlockEntity
	for i := range blocks {
		if strings.Contains(blocks[i].Content, "What is 2+2?") {
			card = &blocks[i]
		}
	}
	if card == nil {
		t.Fatalf("card block not found after re-parse: %+v", blocks)
	}
	if len(card.Children) != 1 || card.Children[0].Content != "4" {
		t.Errorf("expected one child \"4\", got %+v", card.Children)
	}
}

// TestLogseqGraphFixtureLoad loads the checked-in Logseq graph under
// testdata/logseq end-to-end and asserts every offline capability surfaces the
// fixture's content: pages, block references, flashcards, and whiteboards.
func TestLogseqGraphFixtureLoad(t *testing.T) {
	c := NewLogseq("testdata/logseq")
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx := context.Background()

	pages, err := c.GetAllPages(ctx)
	if err != nil {
		t.Fatalf("GetAllPages: %v", err)
	}
	byName := make(map[string]types.PageEntity)
	for _, p := range pages {
		byName[p.Name] = p
	}
	proj, ok := byName["Project"]
	if !ok {
		t.Fatalf("Project page not loaded; got pages %v", byName)
	}
	if proj.Properties["type"] != "project" {
		t.Errorf("Project type property = %v, want \"project\"", proj.Properties["type"])
	}

	// Note's foundational block is referenced once, from Project.
	refs, err := c.GetBlockReferences(ctx, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatalf("GetBlockReferences: %v", err)
	}
	if len(refs) != 1 || refs[0].Page != "Project" {
		t.Errorf("expected 1 reference from Project, got %+v", refs)
	}

	// Two #card blocks: one in Project, one in the journal.
	cards, err := c.GetFlashcards(ctx)
	if err != nil {
		t.Fatalf("GetFlashcards: %v", err)
	}
	if len(cards) != 2 {
		t.Errorf("expected 2 flashcards, got %d: %+v", len(cards), cards)
	}

	// One whiteboard page under whiteboards/.
	boards, err := c.GetWhiteboards(ctx)
	if err != nil {
		t.Fatalf("GetWhiteboards: %v", err)
	}
	if len(boards) != 1 || boards[0].Name != "whiteboards/Canvas" {
		t.Errorf("expected 1 whiteboard whiteboards/Canvas, got %+v", boards)
	}
}

// TestLogseqUUIDStableAcrossReload verifies block identities survive a mutation
// and a full reload from disk: a block with an explicit id:: keeps its UUID, and
// a freshly appended block keeps the UUID returned at append time (persisted as
// an id:: line) when the graph is re-read by a new client.
func TestLogseqUUIDStableAcrossReload(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	stable := "44444444-4444-4444-8444-444444444444"
	body := "- first block\n  id:: " + stable + "\n- second block no id\n"
	if err := os.WriteFile(filepath.Join(dir, "pages", "Page.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewLogseq(dir)
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx := context.Background()

	// Capture the auto-assigned (deterministic) UUID of the id-less block.
	before, err := c.GetPageBlocksTree(ctx, "Page")
	if err != nil {
		t.Fatalf("GetPageBlocksTree: %v", err)
	}
	var autoUUID string
	for _, b := range before {
		if strings.Contains(b.Content, "second block no id") {
			autoUUID = b.UUID
		}
	}
	if autoUUID == "" {
		t.Fatalf("id-less block not found: %+v", before)
	}

	// Append a block — its id:: is persisted to disk.
	appended, err := c.AppendBlockInPage(ctx, "Page", "third block")
	if err != nil {
		t.Fatalf("AppendBlockInPage: %v", err)
	}

	// Re-read from disk with a fresh client.
	c2 := NewLogseq(dir)
	if err := c2.Load(); err != nil {
		t.Fatalf("reload Load: %v", err)
	}
	after, err := c2.GetPageBlocksTree(ctx, "Page")
	if err != nil {
		t.Fatalf("reload GetPageBlocksTree: %v", err)
	}
	got := make(map[string]string) // content -> uuid
	for _, b := range after {
		got[b.Content] = b.UUID
	}
	if got["first block"] != stable {
		t.Errorf("explicit-id block UUID changed across reload: %q want %q", got["first block"], stable)
	}
	if got["third block"] != appended.UUID {
		t.Errorf("appended block UUID not persisted: %q want %q", got["third block"], appended.UUID)
	}
	if got["second block no id"] != autoUUID {
		t.Errorf("id-less block UUID not stable across reload: %q want %q", got["second block no id"], autoUUID)
	}
}

func TestMoveBlockSamePage(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "- first block\n- second block\n- third block\n"
	if err := os.WriteFile(filepath.Join(dir, "pages", "Page.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewLogseq(dir)
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx := context.Background()

	blocks, err := c.GetPageBlocksTree(ctx, "Page")
	if err != nil || len(blocks) != 3 {
		t.Fatalf("GetPageBlocksTree: %d blocks, err %v", len(blocks), err)
	}
	first, third := blocks[0].UUID, blocks[2].UUID

	if err := c.MoveBlock(ctx, third, first, map[string]any{"before": true}); err != nil {
		t.Fatalf("MoveBlock: %v", err)
	}

	blocks, err = c.GetPageBlocksTree(ctx, "Page")
	if err != nil || len(blocks) != 3 {
		t.Fatalf("GetPageBlocksTree after move: %d blocks, err %v", len(blocks), err)
	}
	got := []string{blocks[0].Content, blocks[1].Content, blocks[2].Content}
	want := []string{"third block", "first block", "second block"}
	if got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("block order after move = %v, want %v", got, want)
	}
}

// TestMoveBlockStaleContentErrors guards against a bug where MoveBlock used
// strings.Replace to relocate a block without checking whether the cached
// content actually matched the file. When the in-memory index drifted from
// disk (e.g. a concurrent writer, or a property line the renderer
// re-indents differently than the parser stored it), the anchor replace
// silently no-op'd: the source block got deleted from its old spot and never
// reinserted anywhere, while MoveBlock still returned success. It must now
// fail loudly instead of writing a corrupted file.
func TestMoveBlockStaleContentErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "- first block\n- second block\n"
	if err := os.WriteFile(filepath.Join(dir, "pages", "Page.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewLogseq(dir)
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx := context.Background()

	blocks, err := c.GetPageBlocksTree(ctx, "Page")
	if err != nil || len(blocks) != 2 {
		t.Fatalf("GetPageBlocksTree: %d blocks, err %v", len(blocks), err)
	}
	uuid, targetUUID := blocks[0].UUID, blocks[1].UUID
	srcContent := blocks[0].Content

	// Simulate index drift: the cached target content no longer matches disk.
	c.blockIndex[targetUUID].block.Content = "this text is not actually in the file"

	if err := c.MoveBlock(ctx, uuid, targetUUID, map[string]any{"before": true}); err == nil {
		t.Fatal("expected error when cached content doesn't match file, got nil")
	}

	data, err := os.ReadFile(filepath.Join(dir, "pages", "Page.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), srcContent) {
		t.Error("source block content was removed from disk despite the move failing")
	}
}

func TestLogseqGetWhiteboards(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "whiteboards"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Two whiteboards plus a regular page that must be excluded.
	if err := os.WriteFile(filepath.Join(dir, "whiteboards", "Roadmap.md"), []byte("- canvas\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "whiteboards", "Ideas.md"), []byte("- canvas\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pages", "Notes.md"), []byte("- not a board\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewLogseq(dir)
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	boards, err := c.GetWhiteboards(context.Background())
	if err != nil {
		t.Fatalf("GetWhiteboards: %v", err)
	}
	if len(boards) != 2 {
		t.Fatalf("expected 2 whiteboards, got %d: %+v", len(boards), boards)
	}
	// Sorted by name: "Ideas" before "Roadmap".
	if boards[0].Name != "whiteboards/Ideas" || boards[1].Name != "whiteboards/Roadmap" {
		t.Errorf("unexpected whiteboard names: %+v", boards)
	}
}

func TestReadJournalLayout(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "logseq"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{:journal/file-name-format "yyyy-MM-dd" :preferred-format :markdown}`
	if err := os.WriteFile(filepath.Join(dir, "logseq", "config.edn"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	f := newLogseqFormat(dir)
	if f.journalLayout != "2006-01-02" {
		t.Errorf("journalLayout = %q, want 2006-01-02", f.journalLayout)
	}

	// Default fallback when no config exists.
	f2 := newLogseqFormat(t.TempDir())
	if f2.journalLayout != "2006_01_02" {
		t.Errorf("default journalLayout = %q, want 2006_01_02", f2.journalLayout)
	}
}

// logseqEditFixture is an outliner page with nested, multi-line, and
// duplicate-first-line blocks — the shapes the substring-based editors used
// to mis-locate on disk (tabs for depth, two-space-aligned continuation and
// id:: lines).
const logseqEditFixture = "- parent\n  id:: 11111111-1111-1111-1111-111111111111\n" +
	"\t- nested one\n\t  id:: 22222222-2222-2222-2222-222222222222\n" +
	"\t- multi line\n\t  second line\n\t  id:: 33333333-3333-3333-3333-333333333333\n" +
	"\t\t- grandchild\n" +
	"- other\n  id:: 44444444-4444-4444-4444-444444444444\n" +
	"- nested one\n  id:: 55555555-5555-5555-5555-555555555555\n"

func loadLogseqEditFixture(t *testing.T) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pages", "Page.md")
	if err := os.WriteFile(path, []byte(logseqEditFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewLogseq(dir)
	if err := c.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c, path
}

func readFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLogseqUpdateBlockNested(t *testing.T) {
	c, path := loadLogseqEditFixture(t)
	ctx := context.Background()
	const nested = "22222222-2222-2222-2222-222222222222"

	if err := c.UpdateBlock(ctx, nested, "renamed"); err != nil {
		t.Fatalf("UpdateBlock nested: %v", err)
	}
	got := readFixture(t, path)
	if !strings.Contains(got, "\t- renamed\n\t  id:: "+nested+"\n") {
		t.Errorf("nested block not rewritten in place with depth + id kept:\n%s", got)
	}
	if !strings.Contains(got, "\n- nested one\n  id:: 55555555") {
		t.Errorf("top-level duplicate-text block was touched:\n%s", got)
	}
	if strings.Contains(got, "\nid:: ") {
		t.Errorf("un-indented id:: line spliced into file:\n%s", got)
	}
	blocks, err := c.GetPageBlocksTree(ctx, "Page")
	if err != nil || len(blocks) != 3 || len(blocks[0].Children) != 2 {
		t.Fatalf("tree after update: %d roots, err %v", len(blocks), err)
	}
	if b := blocks[0].Children[0]; b.UUID != nested || b.Content != "renamed" {
		t.Errorf("child = %q/%q, want %q/renamed", b.UUID, b.Content, nested)
	}
}

func TestLogseqUpdateBlockMultiLine(t *testing.T) {
	c, path := loadLogseqEditFixture(t)
	ctx := context.Background()
	const multi = "33333333-3333-3333-3333-333333333333"

	if err := c.UpdateBlock(ctx, multi, "one\ntwo"); err != nil {
		t.Fatalf("UpdateBlock multi-line: %v", err)
	}
	got := readFixture(t, path)
	if !strings.Contains(got, "\t- one\n\t  two\n\t  id:: "+multi+"\n\t\t- grandchild\n") {
		t.Errorf("multi-line block not rewritten with children kept:\n%s", got)
	}
	blocks, _ := c.GetPageBlocksTree(ctx, "Page")
	if b := blocks[0].Children[1]; b.Content != "one\ntwo" || len(b.Children) != 1 {
		t.Errorf("block = %q with %d children, want one\\ntwo with 1 child", b.Content, len(b.Children))
	}
}

func TestLogseqUpdateBlockDuplicateFirstLine(t *testing.T) {
	c, path := loadLogseqEditFixture(t)
	ctx := context.Background()
	const top = "55555555-5555-5555-5555-555555555555"

	if err := c.UpdateBlock(ctx, top, "top renamed"); err != nil {
		t.Fatalf("UpdateBlock top-level: %v", err)
	}
	got := readFixture(t, path)
	if !strings.Contains(got, "\n- top renamed\n  id:: "+top+"\n") {
		t.Errorf("top-level block not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "\t- nested one\n\t  id:: 22222222") {
		t.Errorf("nested block with same text was modified instead:\n%s", got)
	}
}

func TestLogseqUpdateBlockStaleContentErrors(t *testing.T) {
	c, path := loadLogseqEditFixture(t)
	const nested = "22222222-2222-2222-2222-222222222222"
	c.blockIndex[nested].block.Content = "this text is not actually in the file"

	if err := c.UpdateBlock(context.Background(), nested, "renamed"); err == nil {
		t.Fatal("expected error for drifted cache, got nil")
	}
	if got := readFixture(t, path); got != logseqEditFixture {
		t.Errorf("file modified despite failed update:\n%s", got)
	}
}

func TestLogseqRemoveBlockNested(t *testing.T) {
	c, path := loadLogseqEditFixture(t)
	ctx := context.Background()
	const multi = "33333333-3333-3333-3333-333333333333"

	if err := c.RemoveBlock(ctx, multi); err != nil {
		t.Fatalf("RemoveBlock: %v", err)
	}
	got := readFixture(t, path)
	if strings.Contains(got, "multi line") || strings.Contains(got, "grandchild") {
		t.Errorf("block or its children still on disk:\n%s", got)
	}
	if !strings.Contains(got, "\t- nested one\n\t  id:: 22222222") || !strings.Contains(got, "- other\n") {
		t.Errorf("neighbouring blocks damaged:\n%s", got)
	}
	blocks, _ := c.GetPageBlocksTree(ctx, "Page")
	if len(blocks) != 3 || len(blocks[0].Children) != 1 {
		t.Errorf("tree after remove: %d roots, parent has %d children", len(blocks), len(blocks[0].Children))
	}
}

func TestLogseqInsertBlockUnderNestedParent(t *testing.T) {
	c, path := loadLogseqEditFixture(t)
	ctx := context.Background()
	const nested = "22222222-2222-2222-2222-222222222222"

	kid, err := c.InsertBlock(ctx, nested, "kid", nil)
	if err != nil {
		t.Fatalf("InsertBlock: %v", err)
	}
	got := readFixture(t, path)
	if !strings.Contains(got, "\t  id:: "+nested+"\n\t\t- kid\n\t\t  id:: "+kid.UUID+"\n\t- multi line\n") {
		t.Errorf("child not inserted at depth 2 right after parent's own lines:\n%s", got)
	}
	blocks, _ := c.GetPageBlocksTree(ctx, "Page")
	if p := blocks[0].Children[0]; len(p.Children) != 1 || p.Children[0].Content != "kid" {
		t.Errorf("parent children = %+v, want one child kid", p.Children)
	}
}
