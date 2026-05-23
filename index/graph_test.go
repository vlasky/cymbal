package index

import (
	"testing"
	"time"

	"github.com/1broseidon/cymbal/symbols"
)

func TestBuildGraphSymbolModeDirectionsAndStableIDs(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, err := store.UpsertFile("/repo/app/main.go", "app/main.go", "go", "h1", now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "Entry", Kind: "function", File: "/repo/app/main.go", StartLine: 1, EndLine: 20, Language: "go"},
		{Name: "Helper", Kind: "function", File: "/repo/app/main.go", StartLine: 21, EndLine: 30, Language: "go"},
		{Name: "Leaf", Kind: "function", File: "/repo/app/main.go", StartLine: 31, EndLine: 40, Language: "go"},
		{Name: "Caller", Kind: "function", File: "/repo/app/main.go", StartLine: 41, EndLine: 50, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid, []symbols.Ref{
		{Name: "Helper", Line: 2, Language: "go", Kind: symbols.RefKindCall},
		{Name: "Leaf", Line: 22, Language: "go", Kind: symbols.RefKindCall},
		{Name: "Entry", Line: 42, Language: "go", Kind: symbols.RefKindCall},
	}); err != nil {
		t.Fatal(err)
	}

	down, err := store.BuildGraph(GraphQuery{Symbol: "Entry", Direction: GraphDirectionDown, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(down.Edges) != 2 {
		t.Fatalf("expected 2 downward edges, got %+v", down.Edges)
	}
	if down.Nodes[0].ID != graphNodeID(down.Nodes[0].Symbol) {
		t.Fatalf("expected stable node hash id, got %+v", down.Nodes[0])
	}

	up, err := store.BuildGraph(GraphQuery{Symbol: "Entry", Direction: GraphDirectionUp, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(up.Edges) != 1 || up.Edges[0].To != graphNodeID("Entry") {
		t.Fatalf("expected upward caller edge into Entry, got %+v", up.Edges)
	}

	both, err := store.BuildGraph(GraphQuery{Symbol: "Entry", Direction: GraphDirectionBoth, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(both.Edges) != 3 {
		t.Fatalf("expected merged up+down edges, got %+v", both.Edges)
	}
}

func TestBuildGraphSymbolModeScopeExcludeDepthAndUnresolved(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	appID, _ := store.UpsertFile("/repo/app/main.go", "app/main.go", "go", "h1", now, 100)
	libID, _ := store.UpsertFile("/repo/lib/lib.go", "lib/lib.go", "go", "h2", now, 100)
	_ = store.InsertSymbols(appID, []symbols.Symbol{{Name: "Entry", Kind: "function", File: "/repo/app/main.go", StartLine: 1, EndLine: 20, Language: "go"}})
	_ = store.InsertSymbols(libID, []symbols.Symbol{{Name: "Helper", Kind: "function", File: "/repo/lib/lib.go", StartLine: 1, EndLine: 20, Language: "go"}})
	_ = store.InsertRefs(appID, []symbols.Ref{{Name: "Helper", Line: 2, Language: "go", Kind: symbols.RefKindCall}, {Name: "fmt.Printf", Line: 3, Language: "go", Kind: symbols.RefKindCall}})

	graph, err := store.BuildGraph(GraphQuery{Symbol: "Entry", Direction: GraphDirectionDown, Depth: 99, Scope: []string{"app/*"}, IncludeUnresolved: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Edges) != 1 {
		t.Fatalf("expected scoped graph to keep only root->external dashed edge, got %+v", graph.Edges)
	}
	if len(graph.Unresolved) != 1 || graph.Unresolved[0].ResolvedAs != "ext:fmt.Printf" {
		t.Fatalf("expected unresolved ext node, got %+v", graph.Unresolved)
	}

	excluded, err := store.BuildGraph(GraphQuery{Symbol: "Entry", Direction: GraphDirectionDown, Depth: 2, Exclude: []string{"app/*"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(excluded.Nodes) != 0 || len(excluded.Edges) != 0 {
		t.Fatalf("expected exclude to hard-cut graph, got %+v", excluded)
	}
}

// TestBuildGraphRecordsUnresolvedDiagnosticsByDefault is the regression for
// graph diagnostics: even with IncludeUnresolved off (the default), external
// calls must still be recorded in GraphResult.Unresolved — they just aren't
// rendered as ext: nodes/edges. Previously the default filtered them out at the
// trace layer so they never reached the builder.
func TestBuildGraphRecordsUnresolvedDiagnosticsByDefault(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	appID, _ := store.UpsertFile("/repo/app/main.go", "app/main.go", "go", "h1", now, 100)
	libID, _ := store.UpsertFile("/repo/lib/lib.go", "lib/lib.go", "go", "h2", now, 100)
	_ = store.InsertSymbols(appID, []symbols.Symbol{{Name: "Entry", Kind: "function", File: "/repo/app/main.go", StartLine: 1, EndLine: 20, Language: "go"}})
	_ = store.InsertSymbols(libID, []symbols.Symbol{{Name: "Helper", Kind: "function", File: "/repo/lib/lib.go", StartLine: 1, EndLine: 20, Language: "go"}})
	_ = store.InsertRefs(appID, []symbols.Ref{
		{Name: "Helper", Line: 2, Language: "go", Kind: symbols.RefKindCall},     // resolved
		{Name: "fmt.Printf", Line: 3, Language: "go", Kind: symbols.RefKindCall}, // unresolved (external)
	})

	// Default mode: IncludeUnresolved is false.
	graph, err := store.BuildGraph(GraphQuery{Symbol: "Entry", Direction: GraphDirectionDown, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	// Diagnostics are recorded for the external call...
	if len(graph.Unresolved) != 1 || graph.Unresolved[0].ResolvedAs != "ext:fmt.Printf" {
		t.Fatalf("expected fmt.Printf recorded as unresolved diagnostic by default, got %+v", graph.Unresolved)
	}
	// ...but no ext: node is rendered, and the only edge is the resolved one.
	for _, n := range graph.Nodes {
		if n.Kind == GraphNodeKindExternal {
			t.Fatalf("default mode must not render external nodes, got %+v", graph.Nodes)
		}
	}
	if len(graph.Edges) != 1 || !graph.Edges[0].Resolved {
		t.Fatalf("expected a single resolved Entry->Helper edge, got %+v", graph.Edges)
	}
}

// TestBuildGraphResolvesCalleesWithinCallerLanguage verifies the graph builder
// classifies a cross-language name collision consistently with FindTrace: a Go
// call to a name that exists only in Python is an unresolved diagnostic, not a
// resolved edge.
func TestBuildGraphResolvesCalleesWithinCallerLanguage(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	goID, _ := store.UpsertFile("/repo/a.go", "a.go", "go", "h1", now, 100)
	pyID, _ := store.UpsertFile("/repo/b.py", "b.py", "python", "h2", now, 100)
	_ = store.InsertSymbols(goID, []symbols.Symbol{{Name: "Entry", Kind: "function", File: "/repo/a.go", StartLine: 1, EndLine: 20, Language: "go"}})
	_ = store.InsertSymbols(pyID, []symbols.Symbol{{Name: "Render", Kind: "function", File: "/repo/b.py", StartLine: 1, EndLine: 20, Language: "python"}})
	_ = store.InsertRefs(goID, []symbols.Ref{{Name: "Render", Line: 2, Language: "go", Kind: symbols.RefKindCall}})

	// The Go call to Render must not resolve to the Python symbol.
	graph, err := store.BuildGraph(GraphQuery{Symbol: "Entry", Direction: GraphDirectionDown, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range graph.Edges {
		if e.Resolved {
			t.Fatalf("cross-language Render must not produce a resolved edge, got %+v", graph.Edges)
		}
	}
	if len(graph.Unresolved) != 1 || graph.Unresolved[0].ResolvedAs != "ext:Render" {
		t.Fatalf("expected Render recorded as unresolved diagnostic, got %+v", graph.Unresolved)
	}
	// Render exists (in Python), so the diagnostic is scope-filtered, not external.
	if graph.Unresolved[0].Reason != GraphUnresolvedScopeFiltered {
		t.Fatalf("expected scope_filtered reason, got %q", graph.Unresolved[0].Reason)
	}
	for _, n := range graph.Nodes {
		if n.Symbol == "Render" {
			t.Fatalf("Render must not appear as a node in default mode, got %+v", graph.Nodes)
		}
	}

	// Add a Go Render: now it resolves within language and yields a resolved edge.
	_ = store.InsertSymbols(goID, []symbols.Symbol{{Name: "Render", Kind: "function", File: "/repo/a.go", StartLine: 22, EndLine: 30, Language: "go"}})
	graph2, err := store.BuildGraph(GraphQuery{Symbol: "Entry", Direction: GraphDirectionDown, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	var resolvedToRender bool
	for _, e := range graph2.Edges {
		if e.Resolved && e.To == graphNodeID("Render") {
			resolvedToRender = true
		}
	}
	if !resolvedToRender {
		t.Fatalf("same-language Render should produce a resolved edge, got %+v", graph2.Edges)
	}
}

// TestBuildGraphResolveScopeAcrossFamily checks the graph builder honors the
// resolution scope: a Kotlin->Java call resolves under family (default) but not
// under same, and resolves under all.
func TestBuildGraphResolveScopeAcrossFamily(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	ktID, _ := store.UpsertFile("/repo/a.kt", "a.kt", "kotlin", "h1", now, 100)
	javaID, _ := store.UpsertFile("/repo/B.java", "B.java", "java", "h2", now, 100)
	_ = store.InsertSymbols(ktID, []symbols.Symbol{{Name: "Entry", Kind: "function", File: "/repo/a.kt", StartLine: 1, EndLine: 20, Language: "kotlin"}})
	_ = store.InsertSymbols(javaID, []symbols.Symbol{{Name: "Helper", Kind: "method", File: "/repo/B.java", StartLine: 1, EndLine: 20, Language: "java"}})
	_ = store.InsertRefs(ktID, []symbols.Ref{{Name: "Helper", Line: 2, Language: "kotlin", Kind: symbols.RefKindCall}})

	resolvedToHelper := func(g *GraphResult) bool {
		for _, e := range g.Edges {
			if e.Resolved && e.To == graphNodeID("Helper") {
				return true
			}
		}
		return false
	}

	// family (default): JVM interop resolves the edge.
	fam, err := store.BuildGraph(GraphQuery{Symbol: "Entry", Direction: GraphDirectionDown, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !resolvedToHelper(fam) {
		t.Fatalf("family scope should resolve kotlin->java edge, got %+v", fam.Edges)
	}

	// same: out of scope -> scope_filtered diagnostic, no resolved edge.
	same, err := store.BuildGraph(GraphQuery{Symbol: "Entry", Direction: GraphDirectionDown, Depth: 3, ResolveScope: ResolveScopeSame})
	if err != nil {
		t.Fatal(err)
	}
	if resolvedToHelper(same) {
		t.Fatalf("same scope should not resolve kotlin->java edge, got %+v", same.Edges)
	}
	if len(same.Unresolved) != 1 || same.Unresolved[0].Reason != GraphUnresolvedScopeFiltered {
		t.Fatalf("same scope should record a scope_filtered diagnostic, got %+v", same.Unresolved)
	}

	// all: resolves regardless of language.
	all, err := store.BuildGraph(GraphQuery{Symbol: "Entry", Direction: GraphDirectionDown, Depth: 3, ResolveScope: ResolveScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	if !resolvedToHelper(all) {
		t.Fatalf("all scope should resolve kotlin->java edge, got %+v", all.Edges)
	}
}

func TestBuildGraphEmptyGraphIsWellFormed(t *testing.T) {
	store, _ := newTestStore(t)
	graph, err := store.BuildGraph(GraphQuery{Symbol: "Missing", Direction: GraphDirectionDown})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Nodes == nil || graph.Edges == nil || graph.Unresolved == nil {
		t.Fatalf("expected empty slices, got %+v", graph)
	}
	if len(graph.Nodes) != 0 || len(graph.Edges) != 0 || len(graph.Unresolved) != 0 {
		t.Fatalf("expected empty graph, got %+v", graph)
	}
}

func TestBuildGraphLimitAddsSentinelAndTruncatedCount(t *testing.T) {
	graph := &GraphResult{
		Nodes: []GraphNode{
			{ID: graphNodeID("Entry"), Kind: GraphNodeKindSymbol, Label: "Entry", Symbol: "Entry"},
			{ID: graphNodeID("A"), Kind: GraphNodeKindSymbol, Label: "A", Symbol: "A"},
			{ID: graphNodeID("B"), Kind: GraphNodeKindSymbol, Label: "B", Symbol: "B"},
			{ID: graphNodeID("C"), Kind: GraphNodeKindSymbol, Label: "C", Symbol: "C"},
		},
		Edges: []GraphEdge{
			{From: graphNodeID("Entry"), To: graphNodeID("A"), Resolved: true},
			{From: graphNodeID("A"), To: graphNodeID("B"), Resolved: true},
			{From: graphNodeID("A"), To: graphNodeID("C"), Resolved: true},
		},
		Unresolved: []GraphUnresolved{{From: graphNodeID("C"), Key: "fmt.Printf", Reason: GraphUnresolvedExternal}},
	}
	truncated := truncateByDegree(graph, 2, graphNodeID("Entry"))
	if truncated.Truncated != 2 {
		t.Fatalf("expected 2 truncated nodes, got %d", truncated.Truncated)
	}
	if len(truncated.Nodes) != 3 {
		t.Fatalf("expected 2 kept nodes + sentinel, got %+v", truncated.Nodes)
	}
	foundSentinel := false
	foundEntry := false
	for _, n := range truncated.Nodes {
		if n.Kind == GraphNodeKindSentinel {
			foundSentinel = true
		}
		if n.Symbol == "Entry" {
			foundEntry = true
		}
	}
	if !foundSentinel || !foundEntry {
		t.Fatalf("expected sentinel and root preserved, got %+v", truncated.Nodes)
	}
}
