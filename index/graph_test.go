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

// TestBuildGraphResolvesNestedMethodCallee is the regression for the depth>0
// graph-metadata bug: a class-nested method (depth 1) calling another nested
// method must produce a resolved edge, not be mislabeled external. Mirrors the
// real Java parse of `class A { void handle(){ save(); } void save(){} }`.
func TestBuildGraphResolvesNestedMethodCallee(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, _ := store.UpsertFile("/repo/A.java", "A.java", "java", "h", now, 80)
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "A", Kind: "class", File: "/repo/A.java", StartLine: 1, EndLine: 7, Language: "java"},
		{Name: "handle", Kind: "method", File: "/repo/A.java", StartLine: 2, EndLine: 4, Language: "java", Depth: 1, Parent: "A"},
		{Name: "save", Kind: "method", File: "/repo/A.java", StartLine: 5, EndLine: 6, Language: "java", Depth: 1, Parent: "A"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid, []symbols.Ref{
		{Name: "save", Line: 3, Language: "java", Kind: symbols.RefKindCall},
	}); err != nil {
		t.Fatal(err)
	}

	g, err := store.BuildGraph(GraphQuery{Symbol: "handle", Direction: GraphDirectionDown, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range g.Unresolved {
		if u.ResolvedAs == "ext:save" {
			t.Fatalf("nested method 'save' must not be classified %s, got %+v", u.Reason, g.Unresolved)
		}
	}
	var resolved bool
	for _, e := range g.Edges {
		if e.Resolved && e.To == graphNodeID("save") {
			resolved = true
		}
	}
	if !resolved {
		t.Fatalf("expected resolved handle->save edge, got %+v", g.Edges)
	}
}

// TestBuildGraphImpactPreservesNestedCaller is the impact-direction counterpart:
// an upward edge whose caller is a nested method must survive (previously the
// depth>0 caller was dropped by the graph metadata filter).
func TestBuildGraphImpactPreservesNestedCaller(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, _ := store.UpsertFile("/repo/A.java", "A.java", "java", "h", now, 80)
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "A", Kind: "class", File: "/repo/A.java", StartLine: 1, EndLine: 7, Language: "java"},
		{Name: "handle", Kind: "method", File: "/repo/A.java", StartLine: 2, EndLine: 4, Language: "java", Depth: 1, Parent: "A"},
		{Name: "save", Kind: "method", File: "/repo/A.java", StartLine: 5, EndLine: 6, Language: "java", Depth: 1, Parent: "A"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid, []symbols.Ref{
		{Name: "save", Line: 3, Language: "java", Kind: symbols.RefKindCall},
	}); err != nil {
		t.Fatal(err)
	}

	g, err := store.BuildGraph(GraphQuery{Symbol: "save", Direction: GraphDirectionUp, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	var hasCallerEdge bool
	for _, e := range g.Edges {
		if e.Resolved && e.From == graphNodeID("handle") && e.To == graphNodeID("save") {
			hasCallerEdge = true
		}
	}
	if !hasCallerEdge {
		t.Fatalf("expected impact edge handle->save (nested caller preserved), got %+v", g.Edges)
	}
}

// TestBuildGraphAnnotatesAmbiguousNode verifies that a name with more than one
// indexed definition produces a single (name-only) node annotated with the
// definition count and locations, while an unambiguous name carries neither.
func TestBuildGraphAnnotatesAmbiguousNode(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	aID, _ := store.UpsertFile("/repo/a.go", "a.go", "go", "h1", now, 100)
	bID, _ := store.UpsertFile("/repo/b.go", "b.go", "go", "h2", now, 100)
	if err := store.InsertSymbols(aID, []symbols.Symbol{
		{Name: "Root", Kind: "function", File: "/repo/a.go", StartLine: 1, EndLine: 5, Language: "go"},
		{Name: "Dup", Kind: "function", File: "/repo/a.go", StartLine: 6, EndLine: 8, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(bID, []symbols.Symbol{
		{Name: "Dup", Kind: "function", File: "/repo/b.go", StartLine: 3, EndLine: 5, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(aID, []symbols.Ref{
		{Name: "Dup", Line: 2, Language: "go", Kind: symbols.RefKindCall},
	}); err != nil {
		t.Fatal(err)
	}

	g, err := store.BuildGraph(GraphQuery{Symbol: "Root", Direction: GraphDirectionDown, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	var dup, root *GraphNode
	for i := range g.Nodes {
		switch g.Nodes[i].Symbol {
		case "Dup":
			dup = &g.Nodes[i]
		case "Root":
			root = &g.Nodes[i]
		}
	}
	if dup == nil || root == nil {
		t.Fatalf("expected Root and Dup nodes, got %+v", g.Nodes)
	}
	if dup.DefinitionCount != 2 || len(dup.Definitions) != 2 {
		t.Fatalf("expected Dup annotated with 2 definitions, got count=%d defs=%+v", dup.DefinitionCount, dup.Definitions)
	}
	paths := map[string]bool{}
	for _, d := range dup.Definitions {
		paths[d.Path] = true
		if d.StartLine == 0 || d.Language != "go" {
			t.Fatalf("definition missing line/language: %+v", d)
		}
	}
	if !paths["a.go"] || !paths["b.go"] {
		t.Fatalf("expected definitions in a.go and b.go, got %+v", dup.Definitions)
	}
	// The unambiguous Root node carries no annotation.
	if root.DefinitionCount != 0 || root.Definitions != nil {
		t.Fatalf("unambiguous Root should have no annotation, got count=%d defs=%+v", root.DefinitionCount, root.Definitions)
	}
}

// TestBuildGraphSameNameDefinitionSurvivesScopeFilter is the regression for the
// scoped same-name collision: when a name has one definition that's excluded
// and another that's in scope, the node must survive via the in-scope one
// (previously the builder judged visibility off a single arbitrary definition
// and could drop the node entirely).
func TestBuildGraphSameNameDefinitionSurvivesScopeFilter(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	aID, _ := store.UpsertFile("/repo/a.go", "a.go", "go", "h0", now, 100)
	// Two 'Dup' definitions; "excluded/..." sorts before "keep/..." in metas.
	exID, _ := store.UpsertFile("/repo/excluded/dup.go", "excluded/dup.go", "go", "h1", now, 100)
	keepID, _ := store.UpsertFile("/repo/keep/dup.go", "keep/dup.go", "go", "h2", now, 100)
	_ = store.InsertSymbols(aID, []symbols.Symbol{{Name: "Entry", Kind: "function", File: "/repo/a.go", StartLine: 1, EndLine: 5, Language: "go"}})
	_ = store.InsertSymbols(exID, []symbols.Symbol{{Name: "Dup", Kind: "function", File: "/repo/excluded/dup.go", StartLine: 1, EndLine: 3, Language: "go"}})
	_ = store.InsertSymbols(keepID, []symbols.Symbol{{Name: "Dup", Kind: "function", File: "/repo/keep/dup.go", StartLine: 1, EndLine: 3, Language: "go"}})
	_ = store.InsertRefs(aID, []symbols.Ref{{Name: "Dup", Line: 2, Language: "go", Kind: symbols.RefKindCall}})

	g, err := store.BuildGraph(GraphQuery{
		Symbol: "Entry", Direction: GraphDirectionDown, Depth: 3,
		Exclude: []string{"excluded/*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var dup *GraphNode
	for i := range g.Nodes {
		if g.Nodes[i].Symbol == "Dup" {
			dup = &g.Nodes[i]
		}
	}
	if dup == nil {
		t.Fatalf("Dup node should survive via the non-excluded definition, got %+v", g.Nodes)
	}
	if dup.Path != "keep/dup.go" {
		t.Fatalf("Dup node should display the in-scope definition keep/dup.go, got %q", dup.Path)
	}
	var edge bool
	for _, e := range g.Edges {
		if e.Resolved && e.To == graphNodeID("Dup") {
			edge = true
		}
	}
	if !edge {
		t.Fatalf("expected resolved Entry->Dup edge, got %+v", g.Edges)
	}
}

// TestBuildGraphSameNameSurvivesGraphScope is the --graph-scope counterpart to
// the --exclude collision test: when the first-sorted definition is OUTSIDE the
// scope but another is inside, the node must survive via the in-scope one.
func TestBuildGraphSameNameSurvivesGraphScope(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	aID, _ := store.UpsertFile("/repo/a.go", "a.go", "go", "h0", now, 100)
	// "aaa/dup.go" sorts before "keep/dup.go" but is out of --graph-scope.
	outID, _ := store.UpsertFile("/repo/aaa/dup.go", "aaa/dup.go", "go", "h1", now, 100)
	keepID, _ := store.UpsertFile("/repo/keep/dup.go", "keep/dup.go", "go", "h2", now, 100)
	_ = store.InsertSymbols(aID, []symbols.Symbol{{Name: "Entry", Kind: "function", File: "/repo/a.go", StartLine: 1, EndLine: 5, Language: "go"}})
	_ = store.InsertSymbols(outID, []symbols.Symbol{{Name: "Dup", Kind: "function", File: "/repo/aaa/dup.go", StartLine: 1, EndLine: 3, Language: "go"}})
	_ = store.InsertSymbols(keepID, []symbols.Symbol{{Name: "Dup", Kind: "function", File: "/repo/keep/dup.go", StartLine: 1, EndLine: 3, Language: "go"}})
	_ = store.InsertRefs(aID, []symbols.Ref{{Name: "Dup", Line: 2, Language: "go", Kind: symbols.RefKindCall}})

	g, err := store.BuildGraph(GraphQuery{
		Symbol: "Entry", Direction: GraphDirectionDown, Depth: 3,
		Scope: []string{"a.go", "keep/*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var dup *GraphNode
	for i := range g.Nodes {
		if g.Nodes[i].Symbol == "Dup" {
			dup = &g.Nodes[i]
		}
	}
	if dup == nil || dup.Path != "keep/dup.go" {
		t.Fatalf("Dup should survive via in-scope keep/dup.go, got %+v", g.Nodes)
	}
	var edge bool
	for _, e := range g.Edges {
		if e.Resolved && e.To == graphNodeID("Dup") {
			edge = true
		}
	}
	if !edge {
		t.Fatalf("expected resolved Entry->Dup edge, got %+v", g.Edges)
	}
}

// TestBuildGraphImpactSameNameCallerSurvivesExclude exercises the collision fix
// on the impact (up) path: a caller name with one excluded and one in-scope
// definition must still produce the caller->target edge via the in-scope one.
func TestBuildGraphImpactSameNameCallerSurvivesExclude(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	aID, _ := store.UpsertFile("/repo/a.go", "a.go", "go", "h0", now, 100)
	exID, _ := store.UpsertFile("/repo/excluded/caller.go", "excluded/caller.go", "go", "h1", now, 100)
	keepID, _ := store.UpsertFile("/repo/keep/caller.go", "keep/caller.go", "go", "h2", now, 100)
	_ = store.InsertSymbols(aID, []symbols.Symbol{{Name: "Target", Kind: "function", File: "/repo/a.go", StartLine: 1, EndLine: 3, Language: "go"}})
	// Two 'Caller' definitions; the in-scope one (keep/) actually calls Target.
	_ = store.InsertSymbols(exID, []symbols.Symbol{{Name: "Caller", Kind: "function", File: "/repo/excluded/caller.go", StartLine: 1, EndLine: 3, Language: "go"}})
	_ = store.InsertSymbols(keepID, []symbols.Symbol{{Name: "Caller", Kind: "function", File: "/repo/keep/caller.go", StartLine: 1, EndLine: 3, Language: "go"}})
	_ = store.InsertRefs(keepID, []symbols.Ref{{Name: "Target", Line: 2, Language: "go", Kind: symbols.RefKindCall}})

	g, err := store.BuildGraph(GraphQuery{
		Symbol: "Target", Direction: GraphDirectionUp, Depth: 2,
		Exclude: []string{"excluded/*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var caller *GraphNode
	for i := range g.Nodes {
		if g.Nodes[i].Symbol == "Caller" {
			caller = &g.Nodes[i]
		}
	}
	if caller == nil || caller.Path != "keep/caller.go" {
		t.Fatalf("Caller should survive via in-scope keep/caller.go, got %+v", g.Nodes)
	}
	var edge bool
	for _, e := range g.Edges {
		if e.Resolved && e.From == graphNodeID("Caller") && e.To == graphNodeID("Target") {
			edge = true
		}
	}
	if !edge {
		t.Fatalf("expected resolved Caller->Target impact edge, got %+v", g.Edges)
	}
}

// NoTests must contract test-classified caller nodes out of the impact graph
// with hide-but-traverse semantics: the test node disappears, a production
// caller reachable only through it stays connected via an Indirect edge, and
// direct production edges are untouched.
func TestBuildGraphNoTestsContractsTestCallers(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	appID, _ := store.UpsertFile("/repo/app/app.go", "app/app.go", "go", "h1", now, 100)
	if err := store.InsertSymbols(appID, []symbols.Symbol{
		{Name: "Target", Kind: "function", File: "/repo/app/app.go", StartLine: 1, EndLine: 10, Language: "go"},
		{Name: "Direct", Kind: "function", File: "/repo/app/app.go", StartLine: 11, EndLine: 20, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(appID, []symbols.Ref{
		{Name: "Target", Line: 12, Language: "go", Kind: symbols.RefKindCall}, // Direct -> Target
	}); err != nil {
		t.Fatal(err)
	}

	testID, _ := store.UpsertFile("/repo/tests/helper_test.go", "tests/helper_test.go", "go", "h2", now, 100)
	if err := store.InsertSymbols(testID, []symbols.Symbol{
		{Name: "RunScenario", Kind: "function", File: "/repo/tests/helper_test.go", StartLine: 1, EndLine: 10, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(testID, []symbols.Ref{
		{Name: "Target", Line: 2, Language: "go", Kind: symbols.RefKindCall}, // RunScenario -> Target
	}); err != nil {
		t.Fatal(err)
	}

	mainID, _ := store.UpsertFile("/repo/app/main.go", "app/main.go", "go", "h3", now, 100)
	if err := store.InsertSymbols(mainID, []symbols.Symbol{
		{Name: "Main", Kind: "function", File: "/repo/app/main.go", StartLine: 1, EndLine: 10, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(mainID, []symbols.Ref{
		{Name: "RunScenario", Line: 2, Language: "go", Kind: symbols.RefKindCall}, // Main -> RunScenario
	}); err != nil {
		t.Fatal(err)
	}

	// Baseline: without NoTests the test caller is a normal node.
	g, err := store.BuildGraph(GraphQuery{Symbol: "Target", Direction: GraphDirectionUp, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !graphHasNode(g, "RunScenario") {
		t.Fatalf("baseline graph should contain the test caller, got %+v", g.Nodes)
	}

	g, err = store.BuildGraph(GraphQuery{Symbol: "Target", Direction: GraphDirectionUp, Depth: 3, NoTests: true})
	if err != nil {
		t.Fatal(err)
	}
	if graphHasNode(g, "RunScenario") {
		t.Fatalf("NoTests graph must not contain the test caller, got %+v", g.Nodes)
	}
	if !graphHasNode(g, "Main") || !graphHasNode(g, "Direct") {
		t.Fatalf("production callers must survive contraction, got %+v", g.Nodes)
	}
	var mainEdge, directEdge *GraphEdge
	for i := range g.Edges {
		e := &g.Edges[i]
		if e.From == graphNodeID("Main") && e.To == graphNodeID("Target") {
			mainEdge = e
		}
		if e.From == graphNodeID("Direct") && e.To == graphNodeID("Target") {
			directEdge = e
		}
	}
	if mainEdge == nil || !mainEdge.Indirect || !mainEdge.Resolved {
		t.Fatalf("Main must reach Target via a resolved Indirect edge, got %+v", g.Edges)
	}
	if directEdge == nil || directEdge.Indirect {
		t.Fatalf("Direct's edge must stay a plain direct edge, got %+v", g.Edges)
	}
}

func graphHasNode(g *GraphResult, symbol string) bool {
	for _, n := range g.Nodes {
		if n.ID == graphNodeID(symbol) {
			return true
		}
	}
	return false
}
