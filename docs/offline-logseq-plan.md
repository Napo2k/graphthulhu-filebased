# Offline Logseq Mode — Implementation Plan

Status: **done** — all build-order steps (1–8) implemented, tested, and documented.

A third backend, selectable via `--backend logseq-offline` (or
`GRAPHTHULHU_BACKEND=logseq-offline`), that reads/writes a Logseq graph directly
from its markdown files on disk — no running Logseq app, no HTTP endpoint, no
token. Conceptually "Obsidian mode, but speaking Logseq's outliner syntax."

## Decisions (locked)

- **Architecture:** shared core + format strategy (refactor `vault.Client`, not a
  copied package).
- **Scope:** full read-write in the first deliverable.
- **Logseq-only tools offline:** `get_references`, flashcards, and whiteboards all
  in scope. `query_datalog` stays **unsupported** offline (no DataScript DB
  without the app).

## The core difference: outliner vs. headings

| Concern | Obsidian (current `vault`) | Logseq on disk |
|---|---|---|
| Block unit | Heading section (`# H1` … `###### H6`) | Bullet `- ` list item |
| Nesting | Heading level | Tab indentation under a bullet |
| Multi-line block | Lines until next heading | Continuation lines indented to bullet's text column |
| Page properties | YAML frontmatter (`---`) | `key:: value` lines in the **first block** of the file |
| Block properties | n/a | `key:: value` lines inside the block |
| Stable block ID | `<!-- id: UUID -->` HTML comment | `id:: <uuid>` block property |
| Block refs | n/a (Obsidian) | `((uuid))`, `{{embed ((uuid))}}` — already parsed by `parser/content.go:15` |
| File layout | flat `*.md`, daily notes subfolder | `pages/*.md` + `journals/*.md` (date format from `logseq/config.edn`) |
| Namespaces | folders | `parent___child.md` filename → `parent/child` page name |
| Tasks | n/a | `TODO/DOING/DONE/NOW/LATER/WAITING/CANCELLED` + `LOGBOOK`/`SCHEDULED`/`DEADLINE` |
| Datalog | no | **no** (the one real capability loss vs. HTTP Logseq) |

`parser/content.go` already understands Logseq inline syntax (`[[links]]`,
`((refs))`, `#tag`/`#[[tag]]`, `key:: value`, markers, priority), so the *content*
layer is reusable. The work is the **block-tree parser**, the
**serializer/writer**, and **file layout**.

## Architecture: shared core + format strategy

The format-agnostic machinery in `vault/` operates on the cached block tree and is
identical for both formats:

- in-memory index maps, backlink builder (`index.go`), inverted search index
  (`search_index.go`), fsnotify watcher + debounce (`watcher_debounce.go`),
  `safePath`, `atomicWrite`, and all read-serving methods (`GetPage`, `GetBlock`,
  `GetAllPages`, `GetPageLinkedReferences`, and the four searcher interfaces).

Only two things are format-specific: **`parseFile`** (bytes → block tree) and the
**write methods** (mutation → bytes).

Plan: introduce a `format` interface in the `vault` package. Obsidian's current
behavior becomes `obsidianFormat`; the new one is `logseqFormat`. `vault.Client`
retains all index/watch/search/serve logic; `New(...)` takes a `format`. Sketch:

```go
type format interface {
    Parse(relPath, content string, info os.FileInfo) *cachedPage
    PageFilePath(name string) string          // "Foo.md" vs "pages/Foo.md"
    SerializeBlock(b, parent *blockNode) string  // for inserts
    EmbedID(content, uuid string) string      // HTML comment vs id:: prop
    // …minimal surface the write methods need
}
```

The write methods (`vault/vault.go:696-1177`) currently inline heading-specific
string logic; lifting that behind the strategy is the bulk of the refactor risk,
validated by round-trip tests.

**Lighter alternative (rejected):** a standalone `logseqfs` package that copies the
infra. Lands faster but permanently forks ~1400 lines of indexing/watcher code.

## The new Logseq parser & serializer (the real work)

- **Parser** (`logseqParse`): tokenize lines by leading-tab depth; each `- ` opens
  a block; non-bullet indented lines are continuation/properties of the current
  block. Build the `[]types.BlockEntity` tree with an indent-stack (mirrors the
  heading-stack in `markdown.go:51-96`, keyed on tab depth instead of heading
  level). Extract `id::` → `UUID`; pull `key:: value` lines into block properties;
  the file's first block's properties become **page** properties.
- **Serializer**: render the block tree back to bullets with tab indentation,
  properties as `key:: value` lines, preserving `id::`. Writes go through the
  existing `atomicWrite`; the watcher debounce absorbs the temp+rename.
- **File layout**: resolve `pages/` vs `journals/`, decode `___` namespaces, read
  `logseq/config.edn` for the journal date format (minimal EDN read — just
  `:journal/file-name-format` and `:preferred-format`; default `yyyy_MM_dd` +
  markdown).

### Block-ID handling

For blocks lacking an `id::`, generate a deterministic UUID on read (matching the
Obsidian fallback at `vault/markdown.go:134`) but **write the `id::` back on first
mutation** of that block, so `((refs))` stay stable across edits — rather than
rewriting every file on import.

## Capability-interface refactor (`server.go`)

Today `server.go:26` gates `get_references`, `query_datalog`, flashcards, and
whiteboards all behind a single `HasDataScript` check. Offline Logseq has the data
for references/flashcards/whiteboards but **not** Datalog. Split the gate into
finer capability interfaces in `backend/backend.go`:

- `ReferenceSearcher` → `get_references` (offline Logseq: in-memory `((uuid))` scan)
- `FlashcardProvider` → flashcard tools (read `#card` + `card-*::` SRS props)
- `WhiteboardProvider` → whiteboard tools (parse `.whiteboard`/EDN)
- keep `HasDataScript` → **only** `query_datalog`

This is a clean improvement to the existing design and is what lets offline Logseq
surface Logseq-only tools without faking Datalog.

## Build order

1. ✅ Extract the `format` strategy from `vault.Client`; preserve Obsidian behavior
   exactly (green tests gate this — pure refactor, no behavior change).
2. ✅ `logseqFormat` parser: tab-indent outliner tree, `id::` extraction, page/block
   `key:: value` props, `pages/`+`journals/` layout, minimal `config.edn` read,
   `___` namespace decoding.
3. ✅ `logseqFormat` serializer + adapt write methods to outliner output.
4. ✅ Wire `--backend logseq-offline` into the `main.go` switch, reusing
   `LazyBackend` as Obsidian does. (Also fixed a pre-existing duplicate-child bug
   in `tools/navigate.go:enrichBlockTree`.)
5. ✅ Capability-interface split + `get_references` offline. Added
   `backend.ReferenceSearcher` (+ `ReferenceResult`); `LazyLogseqBackend` wrapper
   exposes Logseq-only capabilities so the gate distinguishes offline-Logseq from
   Obsidian (both are `*vault.Client`); live Logseq client implements it via the
   existing DataScript pull; offline scans the block index. `get_references` now
   gates on `ReferenceSearcher`, `query_datalog` stays on `HasDataScript`. UUID
   input validated before query interpolation.
6. ✅ Flashcards (`FlashcardProvider`). Live Logseq pulls `#card` blocks via
   DataScript; offline scans the block index for `#card`/`[[card]]`. Flashcard
   tools gate on `FlashcardProvider`.
7. ✅ Whiteboards (`WhiteboardProvider`) — sequenced last so a regression here
   can't block the rest. Live Logseq pulls `:block/type "whiteboard"` pages (with
   a `whiteboards/` path-scan fallback); offline scans the page index for
   `whiteboards/` pages. Whiteboard tools gate on `WhiteboardProvider`;
   `get_whiteboard` stays backend-agnostic via `GetPageBlocksTree`.
8. ✅ Logseq `testdata` fixtures + graph-load / UUID-stability tests; updated
   `README.md` (backend table, setup) and `CLAUDE.md` (backend list, new gating).

## Testing

- Add `vault/testdata` Logseq fixtures: outliner pages, journals, block props,
  `id::`, nested tasks. Mirror existing `vault/*_test.go` style.
- Round-trip tests: parse → serialize → parse is stable; `id::` UUIDs survive
  edits.
