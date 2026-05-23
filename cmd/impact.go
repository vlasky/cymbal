package cmd

import (
	"fmt"
	"strings"

	"github.com/1broseidon/cymbal/index"
	"github.com/spf13/cobra"
)

var impactCmd = &cobra.Command{
	Use:   "impact <symbol> [symbol2 ...]",
	Short: "Transitive caller analysis — what is impacted if this symbol changes",
	Long: `Find who calls this symbol, recursively, up to --depth.

Multi-symbol: pass more than one name (or pipe via --stdin) to get the union
of callers across all requested symbols. Each caller appears at most once;
a hit_symbols list records which of the requested symbols brought it in.
JSON mode returns a flat list with hit_symbols attribution.

Examples:
  cymbal impact handleRegister                    # single symbol
  cymbal impact Save Load Delete                  # union of callers
  cymbal impact Save Load -D 3                    # deeper chain
  cymbal outline store.go -s --names | cymbal impact --stdin`,
	Args: cobra.MinimumNArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		plan := resolveDBs(cmd)
		ensureFresh(plan.Primary)
		jsonOut := getJSONFlag(cmd)
		depth, _ := cmd.Flags().GetInt("depth")
		limit, _ := cmd.Flags().GetInt("limit")
		ctx, _ := cmd.Flags().GetInt("context")
		scope, err := resolveScopeOrError(cmd)
		if err != nil {
			return err
		}

		names, err := collectSymbols(cmd, args)
		if err != nil {
			return err
		}

		if graphRequested(cmd) {
			// Graph rendering uses a single DB — the seed locator picks
			// whichever federated DB owns the first requested name. Mixed
			// graphs across worktrees would be visually confusing and would
			// violate non-goal #1 (no cross-worktree graph traversal).
			entry, _ := findSymbolEntry(plan, names[0])
			return renderAsGraph(cmd, entry.Path, names, index.GraphDirectionUp, 1)
		}

		// Per-symbol seed-only federation: each name routes to whichever
		// DB owns it, callers stay within that DB.
		merged, sourceMap, labelMap, totalRaw, err := mergeImpactPlan(plan, names, depth, limit, scope)
		if err != nil {
			return err
		}
		_ = labelMap // attached to meta below
		if len(merged) == 0 {
			if len(names) == 1 {
				return fmt.Errorf("no callers found for '%s'", names[0])
			}
			return fmt.Errorf("no callers found for any of: %s", strings.Join(names, ", "))
		}

		ambig := ambiguousSymbolLanguages(plan, names)

		if jsonOut {
			enriched := enrichImpact(merged, ctx)
			// One object shape for any symbol count. Each result carries
			// hit_symbols attribution (which requested symbols brought the
			// caller in); for a single symbol that's just that symbol.
			out := make([]map[string]any, 0, len(enriched))
			for i, row := range enriched {
				out = append(out, map[string]any{
					"row":         row,
					"hit_symbols": sourceMap[impactKey(merged[i])],
				})
			}
			payload := map[string]any{
				"symbols":       names,
				"total_callers": len(merged),
				"raw_rows":      totalRaw,
				"resolve_scope": string(scope),
				"results":       out,
			}
			if len(ambig) > 0 {
				payload["symbol_languages"] = ambig
			}
			return writeJSON(payload)
		}

		// Group by depth.
		maxDepth := 0
		for _, r := range merged {
			if r.Depth > maxDepth {
				maxDepth = r.Depth
			}
		}
		totalGroups := 0
		var content strings.Builder
		for d := 1; d <= maxDepth; d++ {
			var refs []refLine
			for _, r := range merged {
				if r.Depth != d {
					continue
				}
				ctxLines, ctxStart := readSourceContext(r.File, r.Line, ctx)
				label := strings.TrimSpace(readSourceLine(r.File, r.Line))
				if len(names) > 1 {
					if hits := sourceMap[impactKey(r)]; len(hits) > 0 {
						label = fmt.Sprintf("%s  [%s]", label, strings.Join(hits, ","))
					}
				}
				refs = append(refs, refLine{
					relPath:      r.RelPath,
					line:         r.Line,
					text:         label,
					contextLines: ctxLines,
					contextStart: ctxStart,
				})
			}
			if len(refs) == 0 {
				continue
			}
			lines, groups := dedupRefLines(refs)
			totalGroups += groups
			fmt.Fprintf(&content, "# depth %d\n", d)
			for _, l := range lines {
				content.WriteString(l)
				content.WriteByte('\n')
			}
		}

		meta := []kv{}
		if len(names) == 1 {
			meta = append(meta, kv{"symbol", names[0]})
		} else {
			meta = append(meta, kv{"symbols", strings.Join(names, ",")})
		}
		meta = append(meta, kv{"depth", fmt.Sprintf("%d", depth)})
		if totalGroups < len(merged) {
			meta = append(meta, kv{"groups", fmt.Sprintf("%d", totalGroups)})
		}
		meta = append(meta, kv{"total_callers", fmt.Sprintf("%d", len(merged))})
		meta = append(meta, kv{"resolve_scope", string(scope)})
		if s := formatSymbolLanguages(ambig); s != "" {
			meta = append(meta, kv{"symbol_languages", s})
		}
		if len(names) > 1 && totalRaw > len(merged) {
			meta = append(meta, kv{"deduped_from", fmt.Sprintf("%d", totalRaw)})
		}
		if wt := summarizeWorktreeLabels(names, labelMap); wt != "" {
			meta = append(meta, kv{"worktree", wt})
		}
		frontmatter(meta, content.String())
		return nil
	},
}

func init() {
	impactCmd.Flags().IntP("depth", "D", 2, "max call-chain depth (max 5)")
	impactCmd.Flags().IntP("limit", "n", 50, "max results per symbol")
	impactCmd.Flags().IntP("context", "C", 1, "lines of context around each call site (0 for single-line)")
	addStdinFlag(impactCmd)
	addGraphFlags(impactCmd)
	addResolveScopeFlag(impactCmd)
	rootCmd.AddCommand(impactCmd)
}

// impactKey identifies a caller row uniquely enough to deduplicate across
// multiple input symbols. Two rows collide when they point at the same call
// site (file + line + caller identity), which is what we want: the union
// view should surface each real caller exactly once.
func impactKey(r index.ImpactResult) string {
	return fmt.Sprintf("%s:%d|%s", r.File, r.Line, r.Caller)
}

// mergeImpact runs FindImpact for each requested symbol against a single DB.
// Retained for back-compat with existing single-DB callers (tests).
func mergeImpact(dbPath string, names []string, depth, limit int) ([]index.ImpactResult, map[string][]string, int, error) {
	merged, source, _, raw, err := runMergeImpact(names, depth, limit, index.ResolveScopeFamily, func(string) string { return dbPath })
	return merged, source, raw, err
}

// mergeImpactPlan is the federation-aware variant. Each requested name routes
// to whichever DB in plan.Federated owns the seed; downstream callers stay
// within that DB (non-goal #1). labelMap maps each name to its worktree label.
func mergeImpactPlan(plan DBPlan, names []string, depth, limit int, scope index.ResolveScope) ([]index.ImpactResult, map[string][]string, map[string]string, int, error) {
	resolve := func(name string) string {
		entry, _ := findSymbolEntry(plan, name)
		return entry.Path
	}
	labelMap := make(map[string]string, len(names))
	for _, name := range names {
		entry, _ := findSymbolEntry(plan, name)
		labelMap[name] = entry.Label()
	}
	merged, source, _, raw, err := runMergeImpact(names, depth, limit, scope, resolve)
	return merged, source, labelMap, raw, err
}

// runMergeImpact factors the shared dedup logic so the single-DB and
// federated paths don't drift apart over time.
func runMergeImpact(names []string, depth, limit int, scope index.ResolveScope, dbForName func(string) string) ([]index.ImpactResult, map[string][]string, map[string]int, int, error) {
	var merged []index.ImpactResult
	sourceMap := map[string][]string{}
	seen := map[string]int{} // key -> index in merged
	totalRaw := 0
	for _, name := range names {
		dbPath := dbForName(name)
		rows, err := index.FindImpactWithScope(dbPath, name, scope, depth, limit)
		if err != nil {
			return nil, nil, nil, 0, err
		}
		totalRaw += len(rows)
		for _, r := range rows {
			k := impactKey(r)
			if _, ok := seen[k]; !ok {
				seen[k] = len(merged)
				merged = append(merged, r)
			} else {
				// Keep shallowest depth; an indirect contributor shouldn't
				// make a direct caller look deeper than it is.
				idx := seen[k]
				if r.Depth < merged[idx].Depth {
					merged[idx] = r
				}
			}
			// Record attribution without duplicates.
			existing := sourceMap[k]
			dup := false
			for _, e := range existing {
				if e == name {
					dup = true
					break
				}
			}
			if !dup {
				sourceMap[k] = append(existing, name)
			}
		}
	}
	return merged, sourceMap, seen, totalRaw, nil
}

// summarizeWorktreeLabels collapses per-name labels into a single meta
// value: empty when no name came from a non-current worktree, the bare
// label when all non-empty labels agree, or a comma-separated set when
// names came from different worktrees.
func summarizeWorktreeLabels(names []string, labels map[string]string) string {
	seen := make(map[string]struct{}, len(names))
	var ordered []string
	for _, n := range names {
		l := labels[n]
		if l == "" {
			continue
		}
		if _, ok := seen[l]; ok {
			continue
		}
		seen[l] = struct{}{}
		ordered = append(ordered, l)
	}
	return strings.Join(ordered, ",")
}
