# Changelog

All notable changes to cymbal are documented here.

<!-- This page is synced from CHANGELOG.md by the deploy workflow. -->

## [0.12.0] - 2026-04-22

### Added

- **Visual graph output for relationship commands.** `trace`, `impact`, `importers`, and `impls` can now render Mermaid (TTY default), DOT, or JSON graphs directly from the existing verbs.
  ```bash
  cymbal trace <symbol> --graph
  cymbal impact <symbol> --graph
  cymbal importers <file> --graph
  cymbal impls <symbol> --graph
  ```
  - **Shared Graph Flags:**
    - `--graph` — triggers graph mode instead of standard text output.
    - `--graph-format mermaid|dot|json` — overrides the format.
    - `--graph-limit N` — caps output at the top-N most-connected nodes (preserving roots and adding a `_truncated` sentinel).
    - `--include-unresolved` — visualizes external/unindexed relationships (like stdlib imports or conformance to unindexed protocols) as dashed nodes, prefixed with `ext:`.

- **Tightened rendering UX:**
  - Mermaid output automatically caps at 500 nodes to prevent UI rendering lockups, truncating by degree severity and alerting via stderr. You can use `--graph-limit` to dial this in manually.
  - `impact --graph` defaults to a depth of `1` (rather than `impact`'s normal text default of `2`), significantly improving visual readability on hot functions. You can still explicitly pass `--depth 2` to override.

## [0.9.3] - 2026-04-14

### Added

- **Unified language registry** — added a new `lang` package as the single source of truth for language names, file extensions, special filenames, and tree-sitter grammar availability.
- **Broader file recognition** — cymbal now recognizes additional source/config variants during file classification, including `.mjs`, `.cjs`, `.mts`, `.cts`, `.pyw`, `.cxx`, `.hxx`, `.hh`, `.kts`, `.rake`, `.gemspec`, `.sc`, and `.tfvars`.
- **Recognition for non-parseable file types** — cymbal can now classify additional file types for CLI/path heuristics even when they are not indexed, including `Dockerfile`, `Makefile`, `Jenkinsfile`, `CMakeLists.txt`, JSON, TOML, Markdown, SQL, Vue, Svelte, Zig, Erlang, Haskell, OCaml, R, and Perl.

### Changed

- **Shared language resolution across indexing and parsing** — `walker`, `parser`, and `index` now all use the same registry-backed language lookup and parseable-language filtering, reducing drift between file discovery and parser support.
- **Recognized vs parseable languages are now explicit** — indexing walks the parseable subset, while file classification can still identify recognized-but-non-indexable file types.

### Docs

- Updated README agent-integration guidance to reference `AGENTS.md` instead of `agent.md`.

## [0.2.0] - 2026-03-23

### Changed

- All commands now output agent-native frontmatter+content format by default (YAML metadata + content body, optimized for LLM token efficiency)
- `refs` and `impact` deduplicate identical call sites — grouped by file with site count
- `context` callers section uses the same dedup
- `search` results ranked by relevance: exact name match first, then prefix, then contains
- Default limits lowered: refs 50→20, impact 100→50, search 50→20
- `refs`, `impact`, and `context` now show actual source lines at call sites, not just line numbers

## [0.1.0] - 2026-03-23

### Added

- Core indexing engine with tree-sitter parsing, SQLite FTS5 storage, and AI summaries via oneagent
- Batched summarization with diff tracking and model selection
- `cymbal index` — index a codebase
- `cymbal ls` — list files and repo stats
- `cymbal outline` — show file structure
- `cymbal search` — symbol and text search
- `cymbal show` — display symbol source
- `cymbal refs` — find references to a symbol
- `cymbal importers` — reverse import lookup
- `cymbal impact` — transitive caller analysis
- `cymbal diff` — git diff scoped to a symbol
- `cymbal context` — bundled source, callers, and imports in one call

### Fixed

- Overlapping sub-repo detection prevents duplicate symbol indexing
