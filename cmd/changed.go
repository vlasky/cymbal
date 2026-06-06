package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/1broseidon/cymbal/index"
	"github.com/spf13/cobra"
)

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

Changed symbols are attributed by overlapping the diff's changed lines with the
index's symbol ranges. Impact is name-scoped (cymbal resolves references by
name): when a changed name has several definitions the count may span them, and
that ambiguity is reported. Operates only on the current worktree.

Limitations: arbitrary commit ranges whose new side is not the working tree are
not supported (the index reflects the working tree). Deleted files are reported
but their impact is not computed; a deletion inside a file is attributed to the
symbol at the deletion point, which can be a neighbour when a whole symbol is
removed (no old-side line info is available). With --staged, symbols come from
the working tree, so attribution may be off where staged and unstaged edits
overlap. Reference counts are exact (un-truncated) but name-scoped; caller
counts are capped by --limit and flagged when truncated.`,
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
		// core.quotePath=false keeps non-ASCII paths un-escaped so they match
		// the index; gitArgs precede the subcommand.
		gitArgs := append([]string{"-C", repoRoot, "-c", "core.quotePath=false"}, diffArgs...)
		out, err := exec.Command("git", gitArgs...).Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
				return fmt.Errorf("git diff: %s", strings.TrimSpace(string(exitErr.Stderr)))
			}
		}

		// --staged describes the index, but symbols are indexed from the working
		// tree; if unstaged edits coexist, staged line numbers may not line up
		// with indexed symbol ranges. Warn rather than mislead.
		if staged {
			if unstaged, derr := exec.Command("git", "-C", repoRoot, "diff", "--name-only").Output(); derr == nil && len(strings.TrimSpace(string(unstaged))) > 0 {
				fmt.Fprintln(os.Stderr, "warning: working tree has unstaged changes; --staged attribution may be inaccurate where staged and unstaged edits overlap")
			}
		}

		touched, deleted, binaryCount := parseChangedFiles(string(out))

		// Attribute changed lines to symbols, aggregated by name across files.
		byName := map[string]map[string]struct{}{} // name -> set of files
		var order []string
		skippedNoSymbols := 0
		for rel, lines := range touched {
			abs := canonicalExistingPath(filepath.Join(repoRoot, rel))
			syms, ferr := index.FileOutline(dbPath, abs)
			if ferr != nil || len(syms) == 0 {
				skippedNoSymbols++ // changed but unindexed / unsupported / non-code
				continue
			}
			for _, s := range innermostChanged(syms, lines) {
				if byName[s.Name] == nil {
					byName[s.Name] = map[string]struct{}{}
					order = append(order, s.Name)
				}
				byName[s.Name][rel] = struct{}{}
			}
		}
		sort.Strings(order)

		analyzed := order
		symbolsTruncated := false
		if maxSymbols > 0 && len(order) > maxSymbols {
			analyzed = order[:maxSymbols]
			symbolsTruncated = true
		}

		// Per-symbol references + impact, with an aggregate impact-row cap.
		type changedResult struct {
			Symbol          string                `json:"symbol"`
			Files           []string              `json:"files"`
			DefinitionCount int                   `json:"definition_count"`
			References      index.ReferenceCounts `json:"references"`
			ReferencesError bool                  `json:"references_error,omitempty"`
			Impact          *impactSummary        `json:"impact,omitempty"`
			ImpactStatus    string                `json:"impact_status,omitempty"` // "cap" | "error"
		}
		var results []changedResult
		impactTruncated := false
		aggRows := 0
		for _, name := range analyzed {
			res := changedResult{Symbol: name, Files: sortedKeys(byName[name])}
			if syms, derr := index.SymbolsByName(dbPath, name); derr == nil {
				res.DefinitionCount = len(syms)
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
				if tr || (aggLimit > 0 && aggRows >= aggLimit) {
					impactTruncated = true
				}
			}
			results = append(results, res)
		}

		truncated := symbolsTruncated || impactTruncated
		skipped := skippedNoSymbols + binaryCount

		// renderWarnings appends deleted/skipped notes, shown in both the normal
		// and the no-changed-symbols cases so they're never silently lost.
		renderWarnings := func(b *strings.Builder) {
			if len(deleted) == 0 && skipped == 0 {
				return
			}
			b.WriteString("\nwarnings:\n")
			if len(deleted) > 0 {
				fmt.Fprintf(b, "  %d deleted file(s) not analyzed: %s\n", len(deleted), strings.Join(deleted, ", "))
			}
			if skipped > 0 {
				fmt.Fprintf(b, "  %d changed file(s) had no indexed symbols (binary, unindexed, or non-code)\n", skipped)
			}
		}

		if jsonOut {
			payload := map[string]any{
				"base":            baseLabel,
				"changed_symbols": len(order),
				"analyzed":        len(analyzed),
				"truncated":       truncated,
				"resolve_scope":   string(scope),
				"results":         results,
			}
			if len(deleted) > 0 {
				payload["deleted_files"] = deleted
			}
			if skipped > 0 {
				payload["skipped_files"] = skipped
			}
			return writeJSON(payload)
		}

		if len(order) == 0 {
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
			{"changed_symbols", fmt.Sprintf("%d", len(order))},
			{"base", baseLabel},
		}
		if symbolsTruncated {
			meta = append(meta, kv{"analyzed", fmt.Sprintf("%d of %d", len(analyzed), len(order))})
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

// changedDiffArgs builds the git-diff argument list for the requested mode and
// a human label for the comparison base.
func changedDiffArgs(staged bool, base string) ([]string, string, error) {
	common := []string{"diff", "--no-ext-diff", "--find-renames"}
	switch {
	case staged && base != "":
		return nil, "", fmt.Errorf("--staged and --base are mutually exclusive")
	case staged:
		return append(common, "--cached"), "staged", nil
	case base != "":
		if strings.HasPrefix(base, "-") {
			return nil, "", fmt.Errorf("invalid base ref %q", base)
		}
		return append(common, base), base, nil
	default:
		return append(common, "HEAD"), "HEAD", nil
	}
}

// parseChangedFiles walks unified diff output and returns, per new-side file
// path, the set of new-side line numbers that were added or sit at a deletion
// point, plus the paths of fully-deleted files. Attributing on changed lines
// (not whole hunks, which include unchanged context) keeps neighbouring symbols
// from being falsely flagged.
func parseChangedFiles(diff string) (touched map[string]map[int]bool, deleted []string, binary int) {
	touched = map[string]map[int]bool{}

	var curFile, oldPath string
	newLine := 0
	inHunk := false

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "Binary files "):
			binary++
			inHunk = false
		case strings.HasPrefix(line, "--- "):
			oldPath = stripDiffPrefix(strings.TrimPrefix(line, "--- "))
			inHunk = false
		case strings.HasPrefix(line, "+++ "):
			p := strings.TrimPrefix(line, "+++ ")
			if strings.TrimSpace(p) == "/dev/null" {
				if oldPath != "" {
					deleted = append(deleted, oldPath)
				}
				curFile = ""
			} else {
				curFile = stripDiffPrefix(p)
			}
			inHunk = false
		case strings.HasPrefix(line, "@@"):
			newStart, _ := parseHunkHeader(line)
			if newStart > 0 {
				newLine = newStart
				inHunk = true
			}
		case !inHunk || curFile == "":
			// outside a hunk body, or no resolvable new-side file
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" — not a real line
		case strings.HasPrefix(line, "+"):
			mark(touched, curFile, newLine)
			newLine++
		case strings.HasPrefix(line, "-"):
			// Deletion: anchor at the current new-side position so the enclosing
			// symbol is still attributed; new-side line number does not advance.
			mark(touched, curFile, newLine)
		default:
			// context line (leading space) or empty
			newLine++
		}
	}
	return touched, deleted, binary
}

func mark(m map[string]map[int]bool, file string, line int) {
	if line <= 0 {
		return
	}
	if m[file] == nil {
		m[file] = map[int]bool{}
	}
	m[file][line] = true
}

// stripDiffPrefix removes git's a/ or b/ path prefix.
func stripDiffPrefix(p string) string {
	p = strings.TrimRight(p, "\r\n")
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// innermostChanged returns the most specific navigable definition containing
// each changed line (smallest range wins, so a method is preferred over its
// enclosing class), deduplicated by name. Function-local declarations
// (variables, nested types) are skipped so a change inside a function body is
// attributed to the function, not to whatever local sits on that line.
func innermostChanged(syms []index.SymbolResult, lines map[int]bool) []index.SymbolResult {
	seen := map[string]bool{}
	var out []index.SymbolResult
	for line := range lines {
		var best *index.SymbolResult
		for i := range syms {
			s := &syms[i]
			if !isChangedUnit(*s) {
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

// isChangedUnit reports whether a symbol is a navigable definition worth
// reporting as "changed" — something that can have references/callers.
// Class members (methods, constructors) count at any depth; other definitions
// only at the top level, which excludes function-local variables, constants,
// and nested helper types that no external code can reference.
func isChangedUnit(s index.SymbolResult) bool {
	switch s.Kind {
	case "function", "method", "constructor":
		// Functions and methods at any depth. Python and Rust emit methods as
		// kind "function" nested in a class/impl (depth > 0), and those are the
		// real changed units — restricting "function" to depth 0 would
		// mis-attribute every Python/Rust method to its enclosing type.
		// Function-local closures are rare and acceptable noise by comparison.
		return true
	case "class", "struct", "interface", "type",
		"enum", "trait", "protocol", "module", "impl":
		return s.Depth == 0
	default:
		// variables, constants, fields, parameters: navigable only at the top
		// level; function-local declarations are not referenceable definitions.
		return s.Depth == 0
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
