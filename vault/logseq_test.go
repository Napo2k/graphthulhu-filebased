package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
