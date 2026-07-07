package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/1broseidon/cymbal/index"
	"github.com/spf13/cobra"
)

// mermaidNodeCeiling is the hard cap on nodes in a mermaid render. Above
// this, renderers (GitHub, mermaid-live, most in-terminal viewers) begin
// to choke. When the graph would exceed this, we auto-truncate to the
// most-connected nodes and emit a stderr warning. DOT and JSON have no
// ceiling.
const mermaidNodeCeiling = 500

// addGraphFlags wires the shared --graph family of flags onto a command
// that produces edges (trace, impact, importers, impls, etc.). Direction is
// fixed per-verb by the caller.
func addGraphFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("graph", false, "render output as a graph (mermaid on TTY, json when piped)")
	cmd.Flags().String("graph-format", "", "graph output format: mermaid, dot, or json (implies --graph)")
	cmd.Flags().Bool("include-unresolved", false, "include unresolved external calls as dashed ext:<fqn> nodes")
	cmd.Flags().Int("graph-limit", 0, "cap graph at top-N nodes by degree (0 = no cap)")
}

// addResolveScopeFlag wires the shared --resolve-scope flag onto verbs that do
// name-based cross-language resolution (trace, impact, investigate).
func addResolveScopeFlag(cmd *cobra.Command) {
	cmd.Flags().String("resolve-scope", string(index.ResolveScopeFamily),
		"cross-language name resolution: same (exact language) | family (interop group, default) | all (any language)")
}

// resolveScopeFlag reads --resolve-scope, normalizing empty/unknown values to
// the family default. Safe to call on verbs that didn't register the flag
// (returns the family default). Used on the graph render path after the verb's
// RunE has already validated user input via resolveScopeOrError.
func resolveScopeFlag(cmd *cobra.Command) index.ResolveScope {
	if cmd.Flags().Lookup("resolve-scope") == nil {
		return index.ResolveScopeFamily
	}
	raw, _ := cmd.Flags().GetString("resolve-scope")
	return index.NormalizeScope(index.ResolveScope(strings.TrimSpace(raw)))
}

// resolveScopeOrError reads --resolve-scope and rejects unknown values, so an
// agent passing a typo gets a clear error rather than a silent family default.
// Verbs that didn't register the flag get the family default.
func resolveScopeOrError(cmd *cobra.Command) (index.ResolveScope, error) {
	if cmd.Flags().Lookup("resolve-scope") == nil {
		return index.ResolveScopeFamily, nil
	}
	raw, _ := cmd.Flags().GetString("resolve-scope")
	scope := index.ResolveScope(strings.TrimSpace(raw))
	switch scope {
	case index.ResolveScopeSame, index.ResolveScopeFamily, index.ResolveScopeAll:
		return scope, nil
	default:
		return "", fmt.Errorf("invalid --resolve-scope %q: want one of same, family, all", raw)
	}
}

// graphRequested reports whether the user asked for graph output on a verb
// that supports --graph. Returns true if --graph was passed or --graph-format
// was set to a non-empty value.
func graphRequested(cmd *cobra.Command) bool {
	if enabled, _ := cmd.Flags().GetBool("graph"); enabled {
		return true
	}
	if raw, _ := cmd.Flags().GetString("graph-format"); strings.TrimSpace(raw) != "" {
		return true
	}
	return false
}

// selectGraphFormatFromVerb picks the concrete output format for a verb
// that supports --graph. Precedence: --graph-format > --json > TTY default
// (mermaid) / piped default (json).
func selectGraphFormatFromVerb(cmd *cobra.Command) index.GraphFormat {
	if raw, _ := cmd.Flags().GetString("graph-format"); strings.TrimSpace(raw) != "" {
		return index.GraphFormat(strings.TrimSpace(raw))
	}
	if getJSONFlag(cmd) {
		return index.GraphFormatJSON
	}
	if info, err := os.Stdout.Stat(); err == nil && (info.Mode()&os.ModeCharDevice) == 0 {
		return index.GraphFormatJSON
	}
	return index.GraphFormatMermaid
}

// graphDepthOrDefault returns the effective depth for a graph render on a
// verb that already owns a --depth flag. When the user didn't override
// --depth, we use a tighter default for graph output than the text output
// would use — hotspots like impact on frequently-called symbols blow up
// visually at depth 2 but read fine at depth 1.
func graphDepthOrDefault(cmd *cobra.Command, graphDefault int) int {
	if cmd.Flags().Changed("depth") {
		d, _ := cmd.Flags().GetInt("depth")
		return d
	}
	return graphDefault
}

func graphNodeID(parts ...string) string {
	return index.GraphNodeIDFor(strings.Join(parts, "\x1f"))
}

func graphRootIDSet(rootIDs ...string) map[string]bool {
	out := make(map[string]bool, len(rootIDs))
	for _, id := range rootIDs {
		if id != "" {
			out[id] = true
		}
	}
	return out
}

func mergeGraphResults(graphs ...*index.GraphResult) *index.GraphResult {
	merged := &index.GraphResult{
		Nodes:      []index.GraphNode{},
		Edges:      []index.GraphEdge{},
		Unresolved: []index.GraphUnresolved{},
	}
	seenNodes := map[string]bool{}
	seenEdges := map[string]bool{}
	seenUnresolved := map[string]bool{}
	for _, g := range graphs {
		if g == nil {
			continue
		}
		if g.Truncated > 0 {
			merged.Truncated += g.Truncated
		}
		if g.EdgesTruncated {
			merged.EdgesTruncated = true
		}
		for _, n := range g.Nodes {
			if !seenNodes[n.ID] {
				seenNodes[n.ID] = true
				merged.Nodes = append(merged.Nodes, n)
			}
		}
		for _, e := range g.Edges {
			key := e.From + "->" + e.To + "/" + string(e.Kind)
			if !seenEdges[key] {
				seenEdges[key] = true
				merged.Edges = append(merged.Edges, e)
			}
		}
		for _, u := range g.Unresolved {
			key := u.From + "|" + u.Key + "|" + u.ResolvedAs
			if !seenUnresolved[key] {
				seenUnresolved[key] = true
				merged.Unresolved = append(merged.Unresolved, u)
			}
		}
	}
	return merged
}

func renderPreparedGraph(cmd *cobra.Command, graph *index.GraphResult, rootIDs map[string]bool) error {
	format := selectGraphFormatFromVerb(cmd)
	userLimit, _ := cmd.Flags().GetInt("graph-limit")
	graph = applyGraphLimit(graph, userLimit, format, rootIDs)
	// Surface the active resolution scope on verbs that support it (trace,
	// impact); set after merge/limit so it isn't dropped. Verbs without the
	// flag (importers, impls) leave it empty.
	if cmd.Flags().Lookup("resolve-scope") != nil {
		graph.ResolveScope = resolveScopeFlag(cmd)
	}
	return renderGraph(format, graph)
}

// renderAsGraph builds a graph for the given symbols + direction and renders
// it in the format selected by the verb's flags. It merges per-symbol graphs
// by de-duplicating nodes/edges via their stable IDs and applies the
// top-N-by-degree limit (user-supplied via --graph-limit, or the auto mermaid
// ceiling, whichever is tighter).
func renderAsGraph(cmd *cobra.Command, dbPath string, symbols []string, direction index.GraphDirection, graphDefaultDepth int) error {
	if len(symbols) == 0 {
		return renderPreparedGraph(cmd, &index.GraphResult{
			Nodes:      []index.GraphNode{},
			Edges:      []index.GraphEdge{},
			Unresolved: []index.GraphUnresolved{},
		}, nil)
	}
	depth := graphDepthOrDefault(cmd, graphDefaultDepth)
	includeUnresolved, _ := cmd.Flags().GetBool("include-unresolved")
	scope := resolveScopeFlag(cmd)
	// Only impact defines --no-tests; on other verbs the lookup returns false.
	noTests := false
	if cmd.Flags().Lookup("no-tests") != nil {
		noTests, _ = cmd.Flags().GetBool("no-tests")
	}

	graphs := make([]*index.GraphResult, 0, len(symbols))
	rootIDs := make(map[string]bool, len(symbols))
	for _, sym := range symbols {
		q := index.GraphQuery{
			Symbol:            sym,
			Direction:         direction,
			Depth:             depth,
			IncludeUnresolved: includeUnresolved,
			ResolveScope:      scope,
			NoTests:           noTests,
		}
		g, err := index.BuildGraph(dbPath, q)
		if err != nil {
			return fmt.Errorf("graph %q: %w", sym, err)
		}
		graphs = append(graphs, g)
		rootIDs[index.GraphNodeIDFor(sym)] = true
	}
	return renderPreparedGraph(cmd, mergeGraphResults(graphs...), rootIDs)
}

func buildImportersGraph(target string, results []index.ImporterResult) *index.GraphResult {
	rootID := graphNodeID("file", target)
	nodes := map[string]index.GraphNode{
		rootID: {ID: rootID, Kind: index.GraphNodeKindFile, Label: target, Path: target},
	}
	edges := map[string]index.GraphEdge{}
	for _, r := range results {
		parentLabel := r.Parent
		if parentLabel == "" {
			parentLabel = target
		}
		parentID := graphNodeID("file", parentLabel)
		if _, ok := nodes[parentID]; !ok {
			nodes[parentID] = index.GraphNode{ID: parentID, Kind: index.GraphNodeKindFile, Label: parentLabel, Path: parentLabel}
		}
		importerID := graphNodeID("file", r.RelPath)
		nodes[importerID] = index.GraphNode{ID: importerID, Kind: index.GraphNodeKindFile, Label: r.RelPath, Path: r.RelPath}
		key := importerID + "->" + parentID
		edges[key] = index.GraphEdge{From: importerID, To: parentID, Kind: index.GraphEdgeKindImport, Resolved: true}
	}
	return &index.GraphResult{
		Nodes:      sortGraphNodes(mapValues(nodes)),
		Edges:      sortGraphEdges(mapEdgeValues(edges)),
		Unresolved: []index.GraphUnresolved{},
	}
}

func buildImplsGraph(root string, inverse bool, results []index.ImplementorResult, includeUnresolved bool) *index.GraphResult {
	nodes := map[string]index.GraphNode{}
	edges := map[string]index.GraphEdge{}
	unresolved := []index.GraphUnresolved{}

	addResolvedTarget := func(label string) string {
		id := graphNodeID("sym-target", label)
		if _, ok := nodes[id]; !ok {
			nodes[id] = index.GraphNode{ID: id, Kind: index.GraphNodeKindSymbol, Label: label, Symbol: label}
		}
		return id
	}
	addExternalTarget := func(label string) string {
		ext := "ext:" + label
		id := graphNodeID("ext", ext)
		if _, ok := nodes[id]; !ok {
			nodes[id] = index.GraphNode{ID: id, Kind: index.GraphNodeKindExternal, Label: ext, Symbol: ext}
		}
		return id
	}
	addImplementer := func(r index.ImplementorResult) string {
		label := r.Implementer
		if label == "" {
			label = "(anonymous)"
		}
		id := graphNodeID("impl", r.RelPath, fmt.Sprintf("%d", r.Line), label)
		if _, ok := nodes[id]; !ok {
			nodes[id] = index.GraphNode{ID: id, Kind: index.GraphNodeKindSymbol, Label: label, Symbol: label, Path: r.RelPath, Language: r.Language}
		}
		return id
	}
	addRootType := func(label string) string {
		id := graphNodeID("sym-root", label)
		if _, ok := nodes[id]; !ok {
			nodes[id] = index.GraphNode{ID: id, Kind: index.GraphNodeKindSymbol, Label: label, Symbol: label}
		}
		return id
	}

	if inverse {
		rootID := addRootType(root)
		for _, r := range results {
			toID := ""
			resolved := r.Resolved
			if resolved {
				toID = addResolvedTarget(r.Target)
			} else {
				u := index.GraphUnresolved{From: rootID, Key: r.Target, Reason: index.GraphUnresolvedExternal, ResolvedAs: "ext:" + r.Target}
				unresolved = append(unresolved, u)
				if includeUnresolved {
					toID = addExternalTarget(r.Target)
				} else {
					continue
				}
			}
			key := rootID + "->" + toID
			edges[key] = index.GraphEdge{From: rootID, To: toID, Kind: index.GraphEdgeKindImplements, Resolved: resolved}
		}
	} else {
		rootID := addResolvedTarget(root)
		for _, r := range results {
			fromID := addImplementer(r)
			toID := rootID
			resolved := r.Resolved
			if !resolved {
				u := index.GraphUnresolved{From: fromID, Key: root, Reason: index.GraphUnresolvedExternal, ResolvedAs: "ext:" + root}
				unresolved = append(unresolved, u)
				if includeUnresolved {
					toID = addExternalTarget(root)
				} else {
					continue
				}
			}
			key := fromID + "->" + toID
			edges[key] = index.GraphEdge{From: fromID, To: toID, Kind: index.GraphEdgeKindImplements, Resolved: resolved}
		}
	}

	return &index.GraphResult{
		Nodes:      sortGraphNodes(mapValues(nodes)),
		Edges:      sortGraphEdges(mapEdgeValues(edges)),
		Unresolved: sortGraphUnresolved(unresolved),
	}
}

func mapValues(m map[string]index.GraphNode) []index.GraphNode {
	out := make([]index.GraphNode, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func mapEdgeValues(m map[string]index.GraphEdge) []index.GraphEdge {
	out := make([]index.GraphEdge, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func sortGraphNodes(nodes []index.GraphNode) []index.GraphNode {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})
	return nodes
}

func sortGraphEdges(edges []index.GraphEdge) []index.GraphEdge {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return edges
}

func sortGraphUnresolved(rows []index.GraphUnresolved) []index.GraphUnresolved {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].From != rows[j].From {
			return rows[i].From < rows[j].From
		}
		if rows[i].ResolvedAs != rows[j].ResolvedAs {
			return rows[i].ResolvedAs < rows[j].ResolvedAs
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

// applyGraphLimit applies the tighter of the user's --graph-limit and the
// mermaid auto-ceiling (mermaid only). Roots are always kept. Emits a
// stderr warning when the mermaid ceiling bites (the user-set limit is
// self-inflicted, no warning).
func applyGraphLimit(g *index.GraphResult, userLimit int, format index.GraphFormat, rootIDs map[string]bool) *index.GraphResult {
	effective := userLimit
	auto := format == index.GraphFormatMermaid && len(g.Nodes) > mermaidNodeCeiling
	if auto {
		if effective <= 0 || mermaidNodeCeiling < effective {
			effective = mermaidNodeCeiling
		}
	}
	if effective <= 0 || len(g.Nodes) <= effective {
		return g
	}
	truncated := truncateResultByDegree(g, effective, rootIDs)
	if auto && userLimit <= 0 {
		fmt.Fprintf(os.Stderr,
			"warning: mermaid output truncated to %d nodes of %d; pass --graph-format json or --graph-limit N for full graph\n",
			effective, len(g.Nodes))
	}
	return truncated
}

// truncateResultByDegree keeps the top-N nodes ranked by edge degree,
// always preserving any root node ID, and appends a sentinel so the
// truncation is visible in any renderer. This is the cmd-layer mirror of
// index.truncateByDegree — split out because cmd-layer truncation can
// involve multiple roots (merged multi-symbol graphs).
func truncateResultByDegree(g *index.GraphResult, limit int, rootIDs map[string]bool) *index.GraphResult {
	if len(g.Nodes) <= limit {
		return g
	}
	degree := make(map[string]int, len(g.Nodes))
	for _, e := range g.Edges {
		degree[e.From]++
		degree[e.To]++
	}
	type ranked struct {
		node   index.GraphNode
		deg    int
		isRoot bool
	}
	ranks := make([]ranked, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		ranks = append(ranks, ranked{node: n, deg: degree[n.ID], isRoot: rootIDs[n.ID]})
	}
	sort.SliceStable(ranks, func(i, j int) bool {
		if ranks[i].isRoot != ranks[j].isRoot {
			return ranks[i].isRoot
		}
		if ranks[i].deg != ranks[j].deg {
			return ranks[i].deg > ranks[j].deg
		}
		return ranks[i].node.ID < ranks[j].node.ID
	})
	keep := make(map[string]bool, limit)
	kept := make([]index.GraphNode, 0, limit+1)
	for i := 0; i < limit && i < len(ranks); i++ {
		keep[ranks[i].node.ID] = true
		kept = append(kept, ranks[i].node)
	}
	dropped := len(g.Nodes) - len(kept)
	kept = append(kept, index.GraphNode{
		ID:    "_truncated",
		Kind:  index.GraphNodeKindSentinel,
		Label: fmt.Sprintf("… (%d more, truncated)", dropped),
	})
	filteredEdges := make([]index.GraphEdge, 0, len(g.Edges))
	for _, e := range g.Edges {
		if keep[e.From] && keep[e.To] {
			filteredEdges = append(filteredEdges, e)
		}
	}
	filteredUnresolved := make([]index.GraphUnresolved, 0, len(g.Unresolved))
	for _, u := range g.Unresolved {
		if keep[u.From] {
			filteredUnresolved = append(filteredUnresolved, u)
		}
	}
	return &index.GraphResult{
		Nodes:      kept,
		Edges:      filteredEdges,
		Unresolved: filteredUnresolved,
		Truncated:  dropped,
	}
}

func renderGraph(format index.GraphFormat, graph *index.GraphResult) error {
	switch format {
	case index.GraphFormatDot:
		_, err := fmt.Fprint(os.Stdout, renderGraphDOT(graph))
		return err
	case index.GraphFormatMermaid:
		_, err := fmt.Fprint(os.Stdout, renderGraphMermaid(graph))
		return err
	default:
		return writeJSON(graph)
	}
}

// graphNodeLabel is the display label for a node: the symbol name, plus a
// "(N defs)" cue when the name is ambiguous (collapses multiple definitions),
// so the conflation is visible in mermaid/dot. Paths stay out of the visual
// label — they're in the JSON definitions list.
func graphNodeLabel(node index.GraphNode) string {
	if node.DefinitionCount > 1 {
		return fmt.Sprintf("%s (%d defs)", node.Label, node.DefinitionCount)
	}
	return node.Label
}

func renderGraphMermaid(graph *index.GraphResult) string {
	if len(graph.Nodes) == 0 && len(graph.Edges) == 0 {
		return "flowchart LR\n%% no edges\n"
	}
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	for _, node := range graph.Nodes {
		fmt.Fprintf(&b, "  %s[%q]\n", node.ID, graphNodeLabel(node))
	}
	for _, edge := range graph.Edges {
		arrow := "-->"
		if !edge.Resolved || edge.Indirect {
			arrow = "-.->"
		}
		fmt.Fprintf(&b, "  %s %s %s\n", edge.From, arrow, edge.To)
	}
	return b.String()
}

func renderGraphDOT(graph *index.GraphResult) string {
	if len(graph.Nodes) == 0 && len(graph.Edges) == 0 {
		return "digraph cymbal { /* no edges */ }\n"
	}
	var b strings.Builder
	b.WriteString("digraph cymbal {\n")
	for _, node := range graph.Nodes {
		fmt.Fprintf(&b, "  %s [label=%q];\n", node.ID, graphNodeLabel(node))
	}
	for _, edge := range graph.Edges {
		attrs := ""
		if !edge.Resolved || edge.Indirect {
			attrs = " [style=dashed]"
		}
		fmt.Fprintf(&b, "  %s -> %s%s;\n", edge.From, edge.To, attrs)
	}
	b.WriteString("}\n")
	return b.String()
}
