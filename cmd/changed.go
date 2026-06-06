package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/1broseidon/cymbal/index"
	"github.com/1broseidon/cymbal/lang"
	"github.com/1broseidon/cymbal/parser"
	"github.com/1broseidon/cymbal/symbols"
	"github.com/spf13/cobra"
)

// maxChangedParseBytes mirrors the walker's large-file skip threshold so
// on-demand blob parsing never blows up on a giant generated file.
const maxChangedParseBytes = 3407872 // 3.25 MiB

var changedCmd = &cobra.Command{
	Use:   "changed",
	Short: "Diff-scoped impact — what's affected by your current changes",
	Long: `Map a git diff to the symbols it touches, then report each changed
symbol's references and transitive impact in one call.

By default it analyses the working tree against HEAD. Use --staged for the
staged changes, or --base <ref> to diff the working tree against another ref
(e.g. your branch point):

  cymbal changed                 # working tree vs HEAD
  cymbal changed --staged        # staged changes vs HEAD
  cymbal changed --base main     # working tree vs main

Changed symbols are attributed by parsing the actual diffed blobs on both sides:
added/modified lines map to symbols in the new version, deleted lines to symbols
in the old version, so whole-symbol deletions are named (not mis-attributed to a
neighbour) and --staged attribution matches the staged content even with
unstaged edits present. References and impact are then queried from the
working-tree index (the "what's affected now" question), name-scoped (cymbal
resolves references by name): when a changed name has several definitions the
counts may span them, and that is reported as definition_count.

Limitations: arbitrary commit ranges whose new side is not the working tree are
not supported (the index reflects the working tree). Deleted symbols are listed
but have no impact (they no longer exist). Reference counts are exact
(un-truncated) but name-scoped; caller counts are capped by --limit and flagged
when truncated. Operates only on the current worktree.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dbPath := getDBPath(cmd)
		ensureFresh(dbPath)
		jsonOut := getJSONFlag(cmd)
		depth, _ := cmd.Flags().GetInt("depth")
		limit, _ := cmd.Flags().GetInt("limit")
		maxSymbols, _ := cmd.Flags().GetInt("max-symbols")
		aggLimit, _ := cmd.Flags().GetInt("max-impact")
		noTests, _ := cmd.Flags().GetBool("no-tests")
		staged, _ := cmd.Flags().GetBool("staged")
		base, _ := cmd.Flags().GetString("base")
		scope, err := resolveScopeOrError(cmd)
		if err != nil {
			return err
		}

		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		repoRoot, err := gitRepoRoot(cwd)
		if err != nil {
			return fmt.Errorf("not a git repository: %w", err)
		}
		repoRoot = canonicalExistingPath(repoRoot)

		diffArgs, baseLabel, err := changedDiffArgs(staged, base)
		if err != nil {
			return err
		}
		out, err := exec.Command("git", append([]string{"-C", repoRoot}, diffArgs...)...).Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
				return fmt.Errorf("git diff: %s", strings.TrimSpace(string(exitErr.Stderr)))
			}
		}

		// Old-side blob revision per mode: index ("" -> :path) for the default
		// unstaged diff, HEAD for --staged, the ref for --base. This matches the
		// old side git diff itself compares against, so old-side line numbers
		// align with the parsed old blob.
		oldRev := "" // default: the index (unstaged diff compares working tree vs index)
		switch {
		case staged:
			oldRev = "HEAD"
		case base != "":
			oldRev = base
		}

		// Attribute changed lines to symbols by parsing each diffed blob.
		changed := map[string]map[string]struct{}{} // name -> referencing files
		deleted := map[string]map[string]struct{}{} // name -> files it was removed from
		var changedOrder, deletedOrder []string
		add := func(m map[string]map[string]struct{}, order *[]string, name, file string) {
			if m[name] == nil {
				m[name] = map[string]struct{}{}
				*order = append(*order, name)
			}
			if file != "" {
				m[name][file] = struct{}{}
			}
		}

		skipped := 0
		for _, f := range parseChangedFiles(string(out)) {
			if f.Binary {
				skipped++
				continue
			}
			fileParsed := false

			// New side: added/modified symbols, plus the set of names that
			// survive — used to tell a modification from a deletion. Each side is
			// judged by its own path's language (parseBlobSymbols), so an
			// extension-changing rename still parses whichever side is code.
			newNames := map[string]bool{}
			newParsed := false
			if f.NewPath != "" {
				if blob, rerr := readNewSide(repoRoot, staged, f.NewPath); rerr == nil {
					if newSyms, ok := parseBlobSymbols(blob, f.NewPath); ok {
						newParsed = true
						fileParsed = true
						for i := range newSyms {
							newNames[newSyms[i].Name] = true
						}
						for _, s := range innermostChanged(newSyms, f.NewLines) {
							add(changed, &changedOrder, s.Name, f.NewPath)
						}
					}
				}
			}

			// Old side: classify each changed old symbol. It's deleted only when
			// the file is gone (NewPath == "") or the new side parsed cleanly and
			// no longer has that name — never when the new side merely failed to
			// parse, which would manufacture false deletions.
			if f.OldPath != "" && len(f.OldLines) > 0 {
				if blob, rerr := readOldSide(repoRoot, oldRev, f.OldPath); rerr == nil {
					if oldSyms, ok := parseBlobSymbols(blob, f.OldPath); ok {
						fileParsed = true
						for _, s := range innermostChanged(oldSyms, f.OldLines) {
							gone := f.NewPath == "" || (newParsed && !newNames[s.Name])
							if gone {
								add(deleted, &deletedOrder, s.Name, f.OldPath)
							} else {
								// Still present (or new side unknown): a modification.
								// Prefer the new path when the file still exists.
								file := f.NewPath
								if file == "" {
									file = f.OldPath
								}
								add(changed, &changedOrder, s.Name, file)
							}
						}
					}
				}
			}

			if !fileParsed {
				skipped++ // binary, unsupported, oversized, or unreadable on both sides
			}
		}
		sort.Strings(changedOrder)
		sort.Strings(deletedOrder)

		analyzed := changedOrder
		symbolsTruncated := false
		if maxSymbols > 0 && len(changedOrder) > maxSymbols {
			analyzed = changedOrder[:maxSymbols]
			symbolsTruncated = true
		}

		type changedResult struct {
			Symbol          string                `json:"symbol"`
			Files           []string              `json:"files"`
			DefinitionCount int                   `json:"definition_count"`
			Definitions     []defLoc              `json:"definitions,omitempty"`
			Ambiguous       bool                  `json:"ambiguous,omitempty"`
			DefinitionError bool                  `json:"definition_error,omitempty"`
			References      index.ReferenceCounts `json:"references"`
			ReferencesError bool                  `json:"references_error,omitempty"`
			Impact          *impactSummary        `json:"impact,omitempty"`
			ImpactStatus    string                `json:"impact_status,omitempty"` // "cap" | "error"
		}
		var results []changedResult
		impactTruncated := false
		aggRows := 0
		for _, name := range analyzed {
			res := changedResult{Symbol: name, Files: sortedKeys(changed[name])}
			if locs, ok := symbolDefinitions(dbPath, name); ok {
				res.DefinitionCount = len(locs)
				res.Ambiguous = len(locs) > 1
				if res.Ambiguous {
					res.Definitions = locs
				}
			} else {
				res.DefinitionError = true
			}
			if rc, rerr := index.ReferenceCountsWithScope(dbPath, name, scope); rerr == nil {
				res.References = rc
			} else {
				res.ReferencesError = true
			}
			if aggLimit > 0 && aggRows >= aggLimit {
				res.ImpactStatus = "cap"
				impactTruncated = true
			} else if rows, tr, ierr := index.FindImpactWithScope(dbPath, name, scope, depth, limit, noTests); ierr != nil {
				res.ImpactStatus = "error"
			} else {
				prod, test, unknown := classifyImpact(rows)
				res.Impact = &impactSummary{
					TotalCallers:      len(rows),
					ProductionCallers: prod,
					TestCallers:       test,
					UnknownCallers:    unknown,
					Truncated:         tr,
				}
				aggRows += len(rows)
				if tr {
					impactTruncated = true
				}
				// Note: reaching the aggregate cap here is NOT truncation by
				// itself — truncated is set only when a later symbol is actually
				// skipped (the "cap" branch above), so it never fires when the
				// cap is hit exactly on the final symbol with nothing omitted.
			}
			results = append(results, res)
		}

		truncated := symbolsTruncated || impactTruncated

		// deletedList pairs each removed symbol with the file(s) it left.
		type deletedSym struct {
			Symbol string   `json:"symbol"`
			Files  []string `json:"files"`
		}
		var deletedList []deletedSym
		for _, name := range deletedOrder {
			deletedList = append(deletedList, deletedSym{Symbol: name, Files: sortedKeys(deleted[name])})
		}

		if jsonOut {
			payload := map[string]any{
				"base":            baseLabel,
				"changed_symbols": len(changedOrder),
				"analyzed":        len(analyzed),
				"truncated":       truncated,
				"resolve_scope":   string(scope),
				"results":         results,
			}
			if len(deletedList) > 0 {
				payload["deleted_symbols"] = deletedList
			}
			if skipped > 0 {
				payload["skipped_files"] = skipped
			}
			return writeJSON(payload)
		}

		renderWarnings := func(b *strings.Builder) {
			if len(deletedList) == 0 && skipped == 0 {
				return
			}
			b.WriteString("\nwarnings:\n")
			for _, d := range deletedList {
				fmt.Fprintf(b, "  deleted: %s (%s)\n", d.Symbol, strings.Join(d.Files, ", "))
			}
			if skipped > 0 {
				fmt.Fprintf(b, "  %d changed file(s) had no parseable symbols (binary, unsupported, or non-code)\n", skipped)
			}
		}

		if len(changedOrder) == 0 {
			var b strings.Builder
			b.WriteString("No changed symbols found in the diff.\n")
			renderWarnings(&b)
			fmt.Fprint(os.Stderr, b.String())
			return nil
		}

		var content strings.Builder
		for _, res := range results {
			fmt.Fprintf(&content, "# %s  (%s)\n", res.Symbol, strings.Join(res.Files, ", "))
			if res.DefinitionCount > 1 {
				fmt.Fprintf(&content, "  definitions: %d (name-scoped metrics may span them)\n", res.DefinitionCount)
			}
			if res.ReferencesError {
				content.WriteString("  references: unavailable (lookup error)\n")
			} else {
				rc := res.References
				fmt.Fprintf(&content, "  references: %s in %s\n",
					formatCallerCounts(rc.Rows, rc.ProductionRows, rc.TestRows, rc.UnknownRows),
					formatCallerCounts(rc.Files, rc.ProductionFiles, rc.TestFiles, rc.UnknownFiles))
			}
			switch {
			case res.Impact != nil:
				im := res.Impact
				line := fmt.Sprintf("  impact: %s callers",
					formatCallerCounts(im.TotalCallers, im.ProductionCallers, im.TestCallers, im.UnknownCallers))
				if im.Truncated {
					line += "  [truncated]"
				}
				content.WriteString(line + "\n")
			case res.ImpactStatus == "cap":
				content.WriteString("  impact: not computed (max-impact cap reached)\n")
			case res.ImpactStatus == "error":
				content.WriteString("  impact: not computed (lookup error)\n")
			}
		}
		renderWarnings(&content)

		meta := []kv{
			{"changed_symbols", fmt.Sprintf("%d", len(changedOrder))},
			{"base", baseLabel},
		}
		if symbolsTruncated {
			meta = append(meta, kv{"analyzed", fmt.Sprintf("%d of %d", len(analyzed), len(changedOrder))})
		}
		if truncated {
			meta = append(meta, kv{"truncated", "true"})
		}
		meta = append(meta, kv{"resolve_scope", string(scope)})
		frontmatter(meta, content.String())
		return nil
	},
}

// impactSummary is the per-symbol caller rollup embedded in changed output.
type impactSummary struct {
	TotalCallers      int  `json:"total_callers"`
	ProductionCallers int  `json:"production_callers"`
	TestCallers       int  `json:"test_callers"`
	UnknownCallers    int  `json:"unknown_callers"`
	Truncated         bool `json:"truncated"`
}

func init() {
	changedCmd.Flags().Bool("staged", false, "diff staged changes (vs HEAD) instead of the working tree")
	changedCmd.Flags().String("base", "", "diff the working tree against this ref instead of HEAD (e.g. main)")
	changedCmd.Flags().IntP("depth", "D", 2, "max call-chain depth for impact (max 5)")
	changedCmd.Flags().IntP("limit", "n", 50, "max callers per changed symbol")
	changedCmd.Flags().Int("max-symbols", 40, "max changed symbols to analyze (0 = unlimited)")
	changedCmd.Flags().Int("max-impact", 500, "soft cap on total caller rows across symbols; once reached, remaining symbols skip impact (0 = unlimited)")
	changedCmd.Flags().Bool("no-tests", false, "exclude callers in test files from impact")
	addResolveScopeFlag(changedCmd)
	rootCmd.AddCommand(changedCmd)
}

// changedDiffArgs builds the git-diff argument list for the requested mode and a
// human label for the comparison base. core.quotePath=false reduces path
// escaping; remaining C-quoted paths are decoded when parsed.
//
//   default:  git diff           — unstaged: working tree vs index
//   --staged: git diff --cached  — staged:   index vs HEAD
//   --base R: git diff R         — working tree vs ref R
func changedDiffArgs(staged bool, base string) ([]string, string, error) {
	common := []string{"-c", "core.quotePath=false", "diff", "--no-ext-diff", "--find-renames"}
	switch {
	case staged && base != "":
		return nil, "", fmt.Errorf("--staged and --base are mutually exclusive")
	case staged:
		return append(common, "--cached"), "staged", nil
	case base != "":
		if strings.HasPrefix(base, "-") {
			return nil, "", fmt.Errorf("invalid base ref %q", base)
		}
		if strings.Contains(base, "..") {
			// An arbitrary commit range's new side isn't the working tree, so
			// parsing the working tree as "new" would misattribute. Out of scope.
			return nil, "", fmt.Errorf("--base takes a single ref, not a range (%q)", base)
		}
		return append(common, base), base, nil
	default:
		return common, "working tree", nil
	}
}

// changedFile is one file's diff: which old- and new-side line numbers changed,
// and the path on each side ("" when the side is /dev/null).
type changedFile struct {
	OldPath, NewPath string
	OldLines         map[int]bool
	NewLines         map[int]bool
	Binary           bool
}

// parseChangedFiles walks unified diff output into per-file changed-line sets.
// New-side line numbers track '+' (added) lines; old-side track '-' (deleted)
// lines. Attributing on changed lines — never whole hunks (which include
// unchanged context) — keeps neighbouring symbols from being falsely flagged.
func parseChangedFiles(diff string) []changedFile {
	var files []changedFile
	var cur *changedFile
	oldLine, newLine := 0, 0
	inHunk := false
	flush := func() {
		if cur != nil {
			files = append(files, *cur)
			cur = nil
		}
	}

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			cur = &changedFile{OldLines: map[int]bool{}, NewLines: map[int]bool{}}
			inHunk = false
		case cur == nil:
			// preamble before the first file header
		case strings.HasPrefix(line, "Binary files "):
			cur.Binary = true
			inHunk = false
		case strings.HasPrefix(line, "--- "):
			cur.OldPath = diffPathSide(strings.TrimPrefix(line, "--- "))
			inHunk = false
		case strings.HasPrefix(line, "+++ "):
			cur.NewPath = diffPathSide(strings.TrimPrefix(line, "+++ "))
			inHunk = false
		case strings.HasPrefix(line, "@@"):
			if o, n, ok := parseHunkStarts(line); ok {
				oldLine, newLine = o, n
				inHunk = true
			}
		case !inHunk:
			// metadata between header and first hunk (index, mode, rename …)
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file"
		case strings.HasPrefix(line, "+"):
			if newLine > 0 {
				cur.NewLines[newLine] = true
			}
			newLine++
		case strings.HasPrefix(line, "-"):
			if oldLine > 0 {
				cur.OldLines[oldLine] = true
			}
			oldLine++
		default:
			// context line (leading space) or blank
			oldLine++
			newLine++
		}
	}
	flush()
	return files
}

// diffPathSide decodes one side of a diff file header: it un-C-quotes the path
// (git escapes tabs, quotes, backslashes, and — without core.quotePath=false —
// high bytes), maps /dev/null to "", and strips the a/ or b/ prefix. It must
// not trim surrounding whitespace, which can be part of a filename.
func diffPathSide(p string) string {
	p = strings.TrimSuffix(p, "\r")
	if strings.HasPrefix(p, `"`) {
		if dec, err := strconv.Unquote(p); err == nil {
			p = dec
		}
	}
	if p == "/dev/null" {
		return ""
	}
	switch {
	case strings.HasPrefix(p, "a/"):
		return p[2:]
	case strings.HasPrefix(p, "b/"):
		return p[2:]
	}
	return p
}

// parseHunkStarts reads the old- and new-side starting line numbers from a
// unified diff hunk header "@@ -oldStart[,c] +newStart[,c] @@". Zero is a valid
// start (empty side of an add/delete), so callers must not require > 0.
func parseHunkStarts(header string) (oldStart, newStart int, ok bool) {
	minus := strings.IndexByte(header, '-')
	plus := strings.IndexByte(header, '+')
	if minus < 0 || plus < 0 {
		return 0, 0, false
	}
	return leadingInt(header[minus+1:]), leadingInt(header[plus+1:]), true
}

func leadingInt(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}

// readNewSide returns the new-side content: the staged blob under --staged, the
// working-tree file otherwise. A working-tree symlink is rejected — git's blob
// for a symlink is the link target text, so following it would parse the wrong
// file's content.
func readNewSide(repoRoot string, staged bool, path string) ([]byte, error) {
	if staged {
		return catFileBlob(repoRoot, ":"+path)
	}
	full := filepath.Join(repoRoot, path)
	if fi, err := os.Lstat(full); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("symlink: %s", path)
	}
	return os.ReadFile(full)
}

// readOldSide returns the base-revision blob for a path.
func readOldSide(repoRoot, oldRev, path string) ([]byte, error) {
	return catFileBlob(repoRoot, oldRev+":"+path)
}

func catFileBlob(repoRoot, spec string) ([]byte, error) {
	return exec.Command("git", "-C", repoRoot, "cat-file", "blob", spec).Output()
}

// parseBlobSymbols parses in-memory content for a path into symbols. The bool is
// false when the content can't or shouldn't be parsed (too large, binary, a
// recognised-but-not-parseable language, or a parser error) — distinct from a
// successful parse that simply yields no symbols (true, empty slice). Callers
// must not treat a parse failure as "no symbols here", or deletions inferred
// from an empty new side would be wrong. Line numbers are 1-based, matching the
// diff; CRLF is left intact (git and tree-sitter both count LF rows).
func parseBlobSymbols(src []byte, path string) ([]symbols.Symbol, bool) {
	if len(src) > maxChangedParseBytes {
		return nil, false
	}
	if bytes.IndexByte(src, 0) >= 0 {
		return nil, false // binary
	}
	l := lang.Default.ForFile(path)
	if l == nil || !l.Parseable() {
		return nil, false
	}
	src = bytes.TrimPrefix(src, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM (does not shift rows)
	res, err := parser.ParseBytes(src, path, l.Name)
	if err != nil || res == nil {
		return nil, false
	}
	return res.Symbols, true
}

// innermostChanged returns the most specific navigable definition containing
// each changed line (smallest range wins, so a method beats its enclosing
// class), deduplicated by name. Function-local declarations are skipped so a
// change inside a body is attributed to the function, not a local on that line.
func innermostChanged(syms []symbols.Symbol, lines map[int]bool) []symbols.Symbol {
	seen := map[string]bool{}
	var out []symbols.Symbol
	for line := range lines {
		var best *symbols.Symbol
		for i := range syms {
			s := &syms[i]
			if !isChangedUnit(s.Kind, s.Depth) {
				continue
			}
			if s.StartLine <= line && line <= s.EndLine {
				if best == nil || (s.EndLine-s.StartLine) < (best.EndLine-best.StartLine) {
					best = s
				}
			}
		}
		if best != nil && !seen[best.Name] {
			seen[best.Name] = true
			out = append(out, *best)
		}
	}
	return out
}

// isChangedUnit reports whether a (kind, depth) is a navigable definition worth
// reporting as changed — something that can have references/callers. Functions
// and methods count at any depth (Python and Rust emit methods as "function"
// nested in a class/impl); other definitions only at the top level, which
// excludes function-local variables, constants, and nested helper types.
func isChangedUnit(kind string, depth int) bool {
	switch kind {
	case "function", "method", "constructor":
		return true
	case "class", "struct", "interface", "type",
		"enum", "trait", "protocol", "module", "impl":
		return depth == 0
	default:
		return depth == 0
	}
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
