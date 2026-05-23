package index

import (
	"testing"
	"time"

	"github.com/1broseidon/cymbal/symbols"
)

// TestFindImplementorsResolvedVsExternal covers the core behavior of the
// implements relationship: a local class conforms to a local protocol
// (Resolved=true) and a local class conforms to an external framework
// protocol not declared anywhere in the index (Resolved=false). Both show
// up under FindImplementors, with correct Implementer resolution via the
// enclosing-symbol line range.
func TestFindImplementorsResolvedVsExternal(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	// File 1: the local protocol declaration.
	fid1, err := store.UpsertFile("/repo/Named.swift", "Named.swift", "swift", "h1", now, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(fid1, []symbols.Symbol{
		{Name: "Named", Kind: "protocol", File: "/repo/Named.swift", StartLine: 1, EndLine: 3, Language: "swift"},
	}); err != nil {
		t.Fatal(err)
	}

	// File 2: two classes, one conforming to the local "Named" protocol, one
	// to an external "LiveActivityIntent" that is NOT declared in the index.
	fid2, err := store.UpsertFile("/repo/Types.swift", "Types.swift", "swift", "h2", now, 120)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(fid2, []symbols.Symbol{
		{Name: "TimerIntent", Kind: "class", File: "/repo/Types.swift", StartLine: 1, EndLine: 10, Language: "swift"},
		{Name: "NamedTimer", Kind: "class", File: "/repo/Types.swift", StartLine: 12, EndLine: 20, Language: "swift"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid2, []symbols.Ref{
		{Name: "LiveActivityIntent", Line: 1, Language: "swift", Kind: symbols.RefKindImplements},
		{Name: "Named", Line: 12, Language: "swift", Kind: symbols.RefKindImplements},
		// Not an implements edge — must not appear.
		{Name: "LiveActivityIntent", Line: 5, Language: "swift", Kind: symbols.RefKindUse},
	}); err != nil {
		t.Fatal(err)
	}

	// Resolved=false: external protocol.
	ext, err := store.FindImplementors("LiveActivityIntent", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 1 {
		t.Fatalf("expected 1 implementor of LiveActivityIntent, got %d (%+v)", len(ext), ext)
	}
	if ext[0].Implementer != "TimerIntent" {
		t.Errorf("expected implementer=TimerIntent, got %q", ext[0].Implementer)
	}
	if ext[0].Resolved {
		t.Errorf("expected Resolved=false for external protocol, got true")
	}

	// Resolved=true: local protocol declared in file 1.
	local, err := store.FindImplementors("Named", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(local) != 1 {
		t.Fatalf("expected 1 implementor of Named, got %d (%+v)", len(local), local)
	}
	if local[0].Implementer != "NamedTimer" {
		t.Errorf("expected implementer=NamedTimer, got %q", local[0].Implementer)
	}
	if !local[0].Resolved {
		t.Errorf("expected Resolved=true for local protocol, got false")
	}
}

// TestFindImplementsInverse verifies the --of direction: given a type, list
// what it implements. Only implements-kind edges inside the type's line range
// should be returned, not call-kind or use-kind refs.
func TestFindImplementsInverse(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, err := store.UpsertFile("/repo/Repo.ts", "Repo.ts", "typescript", "h", now, 80)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "UserRepo", Kind: "class", File: "/repo/Repo.ts", StartLine: 1, EndLine: 10, Language: "typescript"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid, []symbols.Ref{
		{Name: "BaseRepo", Line: 1, Language: "typescript", Kind: symbols.RefKindImplements},
		{Name: "IUserRepository", Line: 1, Language: "typescript", Kind: symbols.RefKindImplements},
		{Name: "someCall", Line: 5, Language: "typescript", Kind: symbols.RefKindCall},
	}); err != nil {
		t.Fatal(err)
	}

	edges, err := store.FindImplements("UserRepo", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 implements edges, got %d (%+v)", len(edges), edges)
	}
	found := map[string]bool{}
	for _, e := range edges {
		found[e.Target] = true
		if e.Implementer != "UserRepo" {
			t.Errorf("expected Implementer=UserRepo, got %q", e.Implementer)
		}
	}
	if !found["BaseRepo"] || !found["IUserRepository"] {
		t.Errorf("expected BaseRepo + IUserRepository, got %+v", found)
	}
}

// TestFindImplementsSkipsNestedTypeConformances guards against the over-report
// bug where `--of OuterClass` would surface conformances of types nested
// inside it (e.g. Swift `class Session { struct Inner: Proto {} }` must not
// report Proto as one of Session's conformances). The fix: require the
// queried symbol to be the smallest enclosing type-like symbol at the ref's
// line.
func TestFindImplementsSkipsNestedTypeConformances(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, err := store.UpsertFile("/repo/Session.swift", "Session.swift", "swift", "h", now, 400)
	if err != nil {
		t.Fatal(err)
	}
	// Session spans 1-100 and conforms to Sendable on its own declaration line.
	// A private nested struct RequestConvertible (lines 50-60) conforms to
	// URLRequestConvertible. --of Session must return only Sendable.
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "Session", Kind: "class", File: "/repo/Session.swift", StartLine: 1, EndLine: 100, Language: "swift"},
		{Name: "RequestConvertible", Kind: "struct", File: "/repo/Session.swift", StartLine: 50, EndLine: 60, Language: "swift"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid, []symbols.Ref{
		{Name: "Sendable", Line: 1, Language: "swift", Kind: symbols.RefKindImplements},
		{Name: "URLRequestConvertible", Line: 50, Language: "swift", Kind: symbols.RefKindImplements},
	}); err != nil {
		t.Fatal(err)
	}

	// --of Session: should only see Sendable, NOT URLRequestConvertible.
	outer, err := store.FindImplements("Session", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(outer) != 1 {
		t.Fatalf("expected 1 conformance on Session, got %d (%+v)", len(outer), outer)
	}
	if outer[0].Target != "Sendable" {
		t.Errorf("expected target=Sendable, got %q", outer[0].Target)
	}

	// --of RequestConvertible: should see URLRequestConvertible.
	inner, err := store.FindImplements("RequestConvertible", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(inner) != 1 {
		t.Fatalf("expected 1 conformance on RequestConvertible, got %d (%+v)", len(inner), inner)
	}
	if inner[0].Target != "URLRequestConvertible" {
		t.Errorf("expected target=URLRequestConvertible, got %q", inner[0].Target)
	}
}

// TestFindImplementorsRustImplBlock covers the Rust-specific shape where the
// conformance lives in a separate `impl Trait for Type { }` block rather than
// on the type's declaration. The impl_item is indexed as kind=impl with
// name=Type, and both directions (who implements X, what does Type implement)
// must resolve through it.
func TestFindImplementorsRustImplBlock(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, err := store.UpsertFile("/repo/frame.rs", "frame.rs", "rust", "h", now, 120)
	if err != nil {
		t.Fatal(err)
	}
	// struct Frame lives at lines 1-5; its `impl std::error::Error for Frame`
	// block lives separately at lines 10-12. In Rust, conformances are never
	// inside the struct body — they attach to the impl_item.
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "Frame", Kind: "struct", File: "/repo/frame.rs", StartLine: 1, EndLine: 5, Language: "rust"},
		{Name: "Frame", Kind: "impl", File: "/repo/frame.rs", StartLine: 10, EndLine: 12, Language: "rust"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid, []symbols.Ref{
		{Name: "Error", Line: 10, Language: "rust", Kind: symbols.RefKindImplements},
	}); err != nil {
		t.Fatal(err)
	}

	// Incoming: who implements Error? → Frame, via its impl block.
	in, err := store.FindImplementors("Error", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 1 {
		t.Fatalf("expected 1 implementor of Error, got %d (%+v)", len(in), in)
	}
	if in[0].Implementer != "Frame" {
		t.Errorf("expected implementer=Frame (resolved via impl block), got %q", in[0].Implementer)
	}

	// Outgoing: what does Frame implement? → Error, found via the impl block
	// even though `struct Frame` itself has no implements refs inside its body.
	out, err := store.FindImplements("Frame", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 implements edge from Frame, got %d (%+v)", len(out), out)
	}
	if out[0].Target != "Error" {
		t.Errorf("expected target=Error, got %q", out[0].Target)
	}
	if out[0].Implementer != "Frame" {
		t.Errorf("expected implementer=Frame, got %q", out[0].Implementer)
	}
}

// TestFindTraceDefaultFiltersToCallKind is the regression for the Swift noise
// problem: type mentions (Kind=use) must not surface as trace edges by default.
func TestFindTraceDefaultFiltersToCallKind(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, err := store.UpsertFile("/repo/a.swift", "a.swift", "swift", "h", now, 80)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "load", Kind: "function", File: "/repo/a.swift", StartLine: 1, EndLine: 10, Language: "swift"},
		{Name: "fetch", Kind: "function", File: "/repo/a.swift", StartLine: 12, EndLine: 20, Language: "swift"},
		{Name: "UUID", Kind: "type", File: "/repo/a.swift", StartLine: 22, EndLine: 25, Language: "swift"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid, []symbols.Ref{
		// real call inside load() — should surface
		{Name: "fetch", Line: 3, Language: "swift", Kind: symbols.RefKindCall},
		// type mentions — MUST NOT surface in default trace
		{Name: "UUID", Line: 2, Language: "swift", Kind: symbols.RefKindUse},
		{Name: "Date", Line: 4, Language: "swift", Kind: symbols.RefKindUse},
		// implements edges at the declaration line — also must not surface
		{Name: "Sendable", Line: 1, Language: "swift", Kind: symbols.RefKindImplements},
	}); err != nil {
		t.Fatal(err)
	}

	// Default: only "call" edges.
	traces, err := store.FindTrace("load", 2, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range traces {
		if tr.Callee == "UUID" || tr.Callee == "Date" || tr.Callee == "Sendable" {
			t.Errorf("default trace should not surface non-call edge %q (got %+v)", tr.Callee, tr)
		}
	}
	var sawFetch bool
	for _, tr := range traces {
		if tr.Callee == "fetch" {
			sawFetch = true
		}
	}
	if !sawFetch {
		t.Errorf("expected trace to include 'fetch' call; got %+v", traces)
	}

	// Opt-in: widen to call + use, and the type mentions reappear.
	wide, err := store.FindTrace("load", 2, 50, symbols.RefKindCall, symbols.RefKindUse)
	if err != nil {
		t.Fatal(err)
	}
	var sawUUID bool
	for _, tr := range wide {
		if tr.Callee == "UUID" {
			sawUUID = true
		}
	}
	if !sawUUID {
		t.Errorf("expected widened trace to include UUID; got %+v", wide)
	}
}

// TestFindTraceFiltersUnresolvedCalleesByDefault is the regression for the
// default unresolved-filtering behavior: a call to a symbol that isn't in the
// index (stdlib/third-party/builtin) is dropped by default and surfaced only
// when IncludeUnresolved is set.
func TestFindTraceFiltersUnresolvedCalleesByDefault(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, err := store.UpsertFile("/repo/a.go", "a.go", "go", "h", now, 80)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "load", Kind: "function", File: "/repo/a.go", StartLine: 1, EndLine: 10, Language: "go"},
		{Name: "fetch", Kind: "function", File: "/repo/a.go", StartLine: 12, EndLine: 20, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid, []symbols.Ref{
		// resolved: 'fetch' is an indexed symbol.
		{Name: "fetch", Line: 3, Language: "go", Kind: symbols.RefKindCall},
		// unresolved: 'Sprintf' has no indexed symbol (external).
		{Name: "Sprintf", Line: 4, Language: "go", Kind: symbols.RefKindCall},
	}); err != nil {
		t.Fatal(err)
	}

	// Default: resolved 'fetch' kept, unresolved 'Sprintf' dropped.
	def, err := store.FindTrace("load", 2, 50)
	if err != nil {
		t.Fatal(err)
	}
	var defFetch, defSprintf bool
	for _, tr := range def {
		switch tr.Callee {
		case "fetch":
			defFetch = true
		case "Sprintf":
			defSprintf = true
		}
	}
	if !defFetch || defSprintf {
		t.Fatalf("default trace should keep resolved 'fetch' and drop unresolved 'Sprintf', got %+v", def)
	}

	// Opt-in: IncludeUnresolved surfaces the external callee too.
	all, err := store.FindTraceWithOptions("load", 2, 50, TraceOptions{IncludeUnresolved: true})
	if err != nil {
		t.Fatal(err)
	}
	var allFetch, allSprintf bool
	for _, tr := range all {
		switch tr.Callee {
		case "fetch":
			allFetch = true
		case "Sprintf":
			allSprintf = true
		}
	}
	if !allFetch || !allSprintf {
		t.Fatalf("IncludeUnresolved should surface both 'fetch' and 'Sprintf', got %+v", all)
	}
}

// TestFindTraceUnresolvedExemptFromLimit verifies the limit guard: when
// unresolved callees are exempted from the limit (the graph path), resolved
// traversal still reaches full depth even when external calls are abundant —
// they can't crowd out resolved edges within the budget.
func TestFindTraceUnresolvedExemptFromLimit(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, err := store.UpsertFile("/repo/a.go", "a.go", "go", "h", now, 80)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "load", Kind: "function", File: "/repo/a.go", StartLine: 1, EndLine: 10, Language: "go"},
		{Name: "mid", Kind: "function", File: "/repo/a.go", StartLine: 12, EndLine: 20, Language: "go"},
		{Name: "leaf", Kind: "function", File: "/repo/a.go", StartLine: 22, EndLine: 30, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid, []symbols.Ref{
		// abundant unresolved fan-out from load()
		{Name: "ext1", Line: 2, Language: "go", Kind: symbols.RefKindCall},
		{Name: "ext2", Line: 3, Language: "go", Kind: symbols.RefKindCall},
		{Name: "ext3", Line: 4, Language: "go", Kind: symbols.RefKindCall},
		{Name: "ext4", Line: 5, Language: "go", Kind: symbols.RefKindCall},
		// one resolved callee that itself calls a resolved leaf
		{Name: "mid", Line: 6, Language: "go", Kind: symbols.RefKindCall},
		{Name: "leaf", Line: 13, Language: "go", Kind: symbols.RefKindCall},
	}); err != nil {
		t.Fatal(err)
	}

	// limit (3) is smaller than the unresolved fan-out (4). Without the
	// exemption these could consume the budget before resolved traversal
	// reaches 'leaf' at depth 2.
	rows, err := store.FindTraceWithOptions("load", 3, 3, TraceOptions{
		IncludeUnresolved:         true,
		UnresolvedExemptFromLimit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawMid, sawLeaf bool
	for _, tr := range rows {
		switch tr.Callee {
		case "mid":
			sawMid = true
		case "leaf":
			sawLeaf = true
		}
	}
	if !sawMid || !sawLeaf {
		t.Fatalf("resolved traversal (mid->leaf) must survive unresolved fan-out under exemption, got %+v", rows)
	}
}
