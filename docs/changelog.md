# Changelog

All notable changes to cymbal are documented here.

<!-- This page is synced from CHANGELOG.md by the deploy workflow. -->

## [Unreleased]

### Added

- **`--test-path` flag** for `impact` and `changed` — classify repo-specific paths as test code (e.g. `--test-path qa/ --test-path '**/*_it.go'`) in addition to built-in conventions.
- **`--no-tests` honoured in graph mode** — test nodes are contracted; production callers reachable only through test helpers get dashed `"indirect": true` edges.

### Fixed

- `changed`: harden diff parsing against content collisions, config edge cases, and silent failures.
- `classify`: normalize Windows path separators; add missing test-file conventions.
- `trace`/`impact`: honest depth and truncation reporting; stable `investigate --json` output.

---

## [0.14.0] - 2026-06-20

### Added

- **`cymbal changed`** — diff-scoped impact in one command. Maps a git diff to the symbols it touches and reports each one's references and transitive callers. Supports `--staged`, `--base <ref>`, `--no-tests`, and `--max-symbols`/`--max-impact` caps.
- **`cymbal impact` production/test triage** — callers are classified by file path as production, test, or unknown. Header reports real blast radius. `--no-tests` drops test callers.
- **`impact` exact reference metrics** — `references` line / JSON `metrics` block with un-truncated counts of reference sites and distinct files, split by class.
- **`impact`/`trace` truncation flag** — `truncated: true` is shown when `--limit` was hit, so partial results are never presented as complete.
- **Cross-language resolution scope** (`--resolve-scope same|family|all`) for `trace`, `impact`, and `investigate`. Controls whether callees resolve within the same language, same family (JVM, JS, C), or across all languages.
- **Language families** — `lang` package groups interoperable languages (JVM: java/kotlin/scala, JS: javascript/typescript/tsx, C: c/cpp) for name resolution.
- **Ambiguous node annotations** — graph nodes with name collisions carry `definition_count` and `definitions` metadata.
- **`symbol_languages`** — trace/impact JSON reports which languages a name spans when it exists in multiple.
- **`investigate --stdin`** — accept piped symbol names for batch investigation.
- **Unified trace/impact JSON shape** — consistent output structure with scope surfaced in graph results.

### Fixed

- Graph: resolve nested (depth>0) symbols instead of mislabeling external.
- Graph: decide node visibility across all definitions of a name.
- Trace: filter unresolved callees by default (use `--include-unresolved` to show).
- OpenCode: install and load managed plugin correctly.
- Docker: bump base image to golang:1.26-bookworm.

---

## [0.13.5] - 2026-05-19

### Fixed

- Hook nudge now fires on Claude Code's dedicated Grep/Glob/Read tools, not just Bash.
- Update check: cap failure backoff by cache age so stale status resolves faster.
- Test: make worktree fixture portable for Windows CI.

---

## [0.13.4] - 2026-05-19

### Added

- **Worktree federation** — symbol lookup commands fan out across git worktrees sharing the same repo, so queries in one worktree find symbols indexed in another.

---

## [0.13.3] - 2026-05-18

### Fixed

- Hook nudge: suppress on literal-text, known-path, and regex searches (reduces false positives).
- Parser: tighten TS/TSX export signature guards.

---

## [0.13.1] - 2026-05-06

### Added

- **`cymbal hook notify`** — structured update notification payload for agent plugins that want to surface update notices outside hidden system context.
- **Native OS update notifications** for the OpenCode plugin.
- **Cross-platform notification support** (Linux, macOS, Windows).

### Fixed

- Cross-language refs, TSX JSX-prop indexing, Swift field refs, TS export signatures.
- Upgrade Go to 1.26.3 to resolve 10 stdlib security vulnerabilities.

---

## [0.13.0] - 2026-05-06

### Changed

- **Migrated to official tree-sitter Go bindings** — replaces vendored grammars with upstream `go-tree-sitter` packages. Reduces binary size and aligns with upstream grammar releases.

---

## [0.12.2] - 2026-05-02

### Added

- **First-class OpenCode hook installer** (`cymbal hook install opencode`) — managed plugin file with startup guidance and bash nudges.
- **Codecov coverage** — 80% project / 70% patch targets enforced.

### Fixed

- Silence Cobra Usage dump on RunE errors.
- Hook remind text reframed around Claude Code's Grep/Glob/Read tools.
- Use user config dir for OpenCode plugin path.

---

## [0.12.1] - 2026-04-23

### Added

- **Batch symbol queries** — `cymbal search Foo Bar Baz` searches independently in one call.
- **Path operands in search** — trailing path args work like rg: `cymbal search --text <pat> cmd/ internal/foo.go`.
- **Index Python private functions** (leading underscore).

### Fixed

- Checkpoint SQLite WAL on close (prevents journal growth on long sessions).
- Refresh stale update status in reminders.

### Changed

- Refactored graph builder for reduced complexity.

---

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
