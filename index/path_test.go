package index

import (
	"testing"
	"time"

	"github.com/1broseidon/cymbal/symbols"
)

func TestFindPathDirectCall(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, err := store.UpsertFile("/repo/main.go", "main.go", "go", "h1", now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "main", Kind: "function", File: "/repo/main.go", StartLine: 1, EndLine: 10, Language: "go"},
		{Name: "helper", Kind: "function", File: "/repo/main.go", StartLine: 12, EndLine: 20, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid, []symbols.Ref{
		{Name: "helper", Line: 5, Language: "go", Kind: symbols.RefKindCall},
	}); err != nil {
		t.Fatal(err)
	}

	out, err := store.FindPath("main", "helper", 5, TraceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != PathFound {
		t.Fatalf("expected PathFound, got %d", out.Status)
	}
	if len(out.Path) != 1 {
		t.Fatalf("expected 1 hop, got %d: %+v", len(out.Path), out.Path)
	}
	if out.Path[0].Caller != "main" || out.Path[0].Callee != "helper" {
		t.Errorf("unexpected edge: %+v", out.Path[0])
	}
	if out.Path[0].Depth != 1 {
		t.Errorf("expected depth 1, got %d", out.Path[0].Depth)
	}
}

func TestFindPathMultiHop(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, err := store.UpsertFile("/repo/svc.go", "svc.go", "go", "h1", now, 200)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "handleRequest", Kind: "function", File: "/repo/svc.go", StartLine: 1, EndLine: 10, Language: "go"},
		{Name: "validate", Kind: "function", File: "/repo/svc.go", StartLine: 12, EndLine: 20, Language: "go"},
		{Name: "checkToken", Kind: "function", File: "/repo/svc.go", StartLine: 22, EndLine: 30, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid, []symbols.Ref{
		{Name: "validate", Line: 5, Language: "go", Kind: symbols.RefKindCall},
		{Name: "checkToken", Line: 15, Language: "go", Kind: symbols.RefKindCall},
	}); err != nil {
		t.Fatal(err)
	}

	out, err := store.FindPath("handleRequest", "checkToken", 5, TraceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != PathFound {
		t.Fatalf("expected PathFound, got %d", out.Status)
	}
	if len(out.Path) != 2 {
		t.Fatalf("expected 2 hops, got %d: %+v", len(out.Path), out.Path)
	}
	if out.Path[0].Caller != "handleRequest" || out.Path[0].Callee != "validate" {
		t.Errorf("hop 1 unexpected: %+v", out.Path[0])
	}
	if out.Path[1].Caller != "validate" || out.Path[1].Callee != "checkToken" {
		t.Errorf("hop 2 unexpected: %+v", out.Path[1])
	}
	if out.Path[0].Depth != 1 || out.Path[1].Depth != 2 {
		t.Errorf("depths wrong: %d, %d", out.Path[0].Depth, out.Path[1].Depth)
	}
}

func TestFindPathNotReachable(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, err := store.UpsertFile("/repo/isolated.go", "isolated.go", "go", "h1", now, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "alpha", Kind: "function", File: "/repo/isolated.go", StartLine: 1, EndLine: 5, Language: "go"},
		{Name: "beta", Kind: "function", File: "/repo/isolated.go", StartLine: 7, EndLine: 12, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}

	out, err := store.FindPath("alpha", "beta", 5, TraceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != PathNotReachable {
		t.Errorf("expected PathNotReachable, got %d", out.Status)
	}
}

func TestFindPathSameSymbolReturnsEmpty(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	fid, err := store.UpsertFile("/repo/self.go", "self.go", "go", "h1", now, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "foo", Kind: "function", File: "/repo/self.go", StartLine: 1, EndLine: 5, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}

	out, err := store.FindPath("foo", "foo", 5, TraceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != PathFound {
		t.Fatalf("expected PathFound for same symbol, got %d", out.Status)
	}
	if len(out.Path) != 0 {
		t.Errorf("expected 0 hops for same symbol, got %d", len(out.Path))
	}
}

func TestFindPathSymbolNotFound(t *testing.T) {
	store, _ := newTestStore(t)

	_, err := store.FindPath("nonexistent", "alsoMissing", 5, TraceOptions{})
	if err == nil {
		t.Fatal("expected error for missing symbol")
	}
}

func TestFindPathDepthExhausted(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	// Chain: chainA -> chainB -> chainC -> chainD
	fid, err := store.UpsertFile("/repo/chain.go", "chain.go", "go", "h1", now, 200)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(fid, []symbols.Symbol{
		{Name: "chainA", Kind: "function", File: "/repo/chain.go", StartLine: 1, EndLine: 10, Language: "go"},
		{Name: "chainB", Kind: "function", File: "/repo/chain.go", StartLine: 12, EndLine: 20, Language: "go"},
		{Name: "chainC", Kind: "function", File: "/repo/chain.go", StartLine: 22, EndLine: 30, Language: "go"},
		{Name: "chainD", Kind: "function", File: "/repo/chain.go", StartLine: 32, EndLine: 40, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(fid, []symbols.Ref{
		{Name: "chainB", Line: 5, Language: "go", Kind: symbols.RefKindCall},
		{Name: "chainC", Line: 15, Language: "go", Kind: symbols.RefKindCall},
		{Name: "chainD", Line: 25, Language: "go", Kind: symbols.RefKindCall},
	}); err != nil {
		t.Fatal(err)
	}

	// Depth 2: can't reach chainD (3 hops away), but frontier is not empty.
	out, err := store.FindPath("chainA", "chainD", 2, TraceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != PathDepthExhausted {
		t.Errorf("expected PathDepthExhausted at depth 2, got %d", out.Status)
	}

	// Depth 3: should find it.
	out, err = store.FindPath("chainA", "chainD", 3, TraceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != PathFound {
		t.Fatalf("expected PathFound at depth 3, got %d", out.Status)
	}
	if len(out.Path) != 3 {
		t.Fatalf("expected 3 hops, got %d", len(out.Path))
	}
}

func TestFindPathRespectsResolveScope(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Now()

	// Go file with a function calling "parse"
	goFid, err := store.UpsertFile("/repo/handler.go", "handler.go", "go", "h1", now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(goFid, []symbols.Symbol{
		{Name: "handle", Kind: "function", File: "/repo/handler.go", StartLine: 1, EndLine: 10, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRefs(goFid, []symbols.Ref{
		{Name: "parse", Line: 5, Language: "go", Kind: symbols.RefKindCall},
	}); err != nil {
		t.Fatal(err)
	}

	// "parse" only exists in Python — different family from Go.
	pyFid, err := store.UpsertFile("/repo/util.py", "util.py", "python", "h2", now, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSymbols(pyFid, []symbols.Symbol{
		{Name: "parse", Kind: "function", File: "/repo/util.py", StartLine: 1, EndLine: 5, Language: "python"},
	}); err != nil {
		t.Fatal(err)
	}

	// With family scope: Go caller can't resolve Python callee.
	out, err := store.FindPath("handle", "parse", 5, TraceOptions{Scope: ResolveScopeFamily})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status == PathFound {
		t.Errorf("family scope should not resolve cross-family, got path: %+v", out.Path)
	}

	// With "all" scope: should resolve.
	out, err = store.FindPath("handle", "parse", 5, TraceOptions{Scope: ResolveScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != PathFound {
		t.Fatalf("scope=all should find cross-language path, got status %d", out.Status)
	}
	if len(out.Path) != 1 {
		t.Fatalf("expected 1 hop, got %d", len(out.Path))
	}
}
