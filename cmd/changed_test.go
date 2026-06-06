package cmd

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/1broseidon/cymbal/index"
	"github.com/1broseidon/cymbal/symbols"
)

// BenchmarkParseChangedFiles measures diff parsing on a large changeset
// (200 files x 5 hunks) — the per-diff scaling cost of `cymbal changed`.
func BenchmarkParseChangedFiles(b *testing.B) {
	var sb strings.Builder
	for f := 0; f < 200; f++ {
		fmt.Fprintf(&sb, "diff --git a/file%d.go b/file%d.go\n--- a/file%d.go\n+++ b/file%d.go\n", f, f, f, f)
		for h := 0; h < 5; h++ {
			start := h*20 + 1
			fmt.Fprintf(&sb, "@@ -%d,3 +%d,4 @@\n ctx\n+added one\n+added two\n ctx\n", start, start)
		}
	}
	diff := sb.String()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parseChangedFiles(diff)
	}
}

// BenchmarkParseBlobSymbols measures the on-demand parse cost for one large
// source blob (500 functions), the dominant per-file cost in `cymbal changed`.
func BenchmarkParseBlobSymbols(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("package p\n")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&sb, "func F%d() int { x := %d; return x }\n", i, i)
	}
	src := []byte(sb.String())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := parseBlobSymbols(src, "p.go"); !ok {
			b.Fatal("parse failed")
		}
	}
}

func TestParseChangedFilesTracksOldAndNewLines(t *testing.T) {
	diff := `diff --git a/foo.go b/foo.go
index abc..def 100644
--- a/foo.go
+++ b/foo.go
@@ -10,3 +10,4 @@ func X() {
 ctx line
+added1
+added2
 ctx line
@@ -20,2 +21,1 @@
-deleted
 ctx
diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-a
-b
diff --git a/logo.png b/logo.png
index 1111..2222 100644
Binary files a/logo.png and b/logo.png differ
`
	files := parseChangedFiles(diff)
	byNew := map[string]changedFile{}
	for _, f := range files {
		key := f.NewPath
		if key == "" {
			key = f.OldPath
		}
		byNew[key] = f
	}

	foo := byNew["foo.go"]
	if got, want := sortedInts(foo.NewLines), []int{11, 12}; !reflect.DeepEqual(got, want) {
		t.Errorf("foo.go new lines = %v, want %v", got, want)
	}
	if got, want := sortedInts(foo.OldLines), []int{20}; !reflect.DeepEqual(got, want) {
		t.Errorf("foo.go old lines = %v, want %v (deletion on old side)", got, want)
	}

	gone := byNew["gone.go"]
	if gone.NewPath != "" {
		t.Errorf("gone.go new path = %q, want empty (/dev/null)", gone.NewPath)
	}
	if got, want := sortedInts(gone.OldLines), []int{1, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("gone.go old lines = %v, want %v", got, want)
	}

	// Binary files have no ---/+++ path lines, so the flag rides an entry with
	// empty paths; RunE counts those as skipped.
	binaryCount := 0
	for _, f := range files {
		if f.Binary {
			binaryCount++
		}
	}
	if binaryCount != 1 {
		t.Errorf("binary files flagged = %d, want 1", binaryCount)
	}
}

func TestDiffPathSideDecodesQuotedAndStripsPrefix(t *testing.T) {
	cases := map[string]string{
		"a/foo.go":          "foo.go",
		"b/pkg/bar.go":      "pkg/bar.go",
		"/dev/null":         "",
		`"b/with space.go"`: "with space.go",
		`"a/tab\there.go"`:  "tab\there.go",
		"a/b/keep.go":       "b/keep.go", // only the leading a/ is stripped
	}
	for in, want := range cases {
		if got := diffPathSide(in); got != want {
			t.Errorf("diffPathSide(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseHunkStarts(t *testing.T) {
	cases := []struct {
		header             string
		oldStart, newStart int
	}{
		{"@@ -10,3 +12,4 @@", 10, 12},
		{"@@ -1 +1 @@", 1, 1},
		{"@@ -0,0 +1,5 @@ new file", 0, 1},
		{"@@ -7,2 +0,0 @@", 7, 0},
	}
	for _, c := range cases {
		o, n, ok := parseHunkStarts(c.header)
		if !ok || o != c.oldStart || n != c.newStart {
			t.Errorf("parseHunkStarts(%q) = (%d,%d,%v), want (%d,%d,true)", c.header, o, n, ok, c.oldStart, c.newStart)
		}
	}
}

func TestInnermostChangedSkipsLocalsPrefersInnerDefinition(t *testing.T) {
	syms := []symbols.Symbol{
		{Name: "DoWork", Kind: "function", Depth: 0, StartLine: 10, EndLine: 20},
		{Name: "local", Kind: "variable", Depth: 1, StartLine: 15, EndLine: 15},
		{Name: "Widget", Kind: "class", Depth: 0, StartLine: 30, EndLine: 60},
		{Name: "render", Kind: "method", Depth: 1, StartLine: 40, EndLine: 50},
	}

	// Line 15 sits on a local var inside DoWork: attribute to DoWork, not the var.
	// Line 45 sits in a method inside a class: attribute to the method, not the class.
	out := innermostChanged(syms, map[int]bool{15: true, 45: true})
	names := []string{}
	for _, s := range out {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	want := []string{"DoWork", "render"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("innermostChanged names = %v, want %v", names, want)
	}
}

func TestIsChangedUnit(t *testing.T) {
	cases := []struct {
		kind  string
		depth int
		want  bool
	}{
		{"function", 0, true},
		{"function", 1, true}, // Python/Rust method
		{"method", 1, true},
		{"variable", 0, true},  // top-level var
		{"variable", 1, false}, // local var
		{"type", 1, false},     // function-local type
		{"type", 0, true},
		{"constant", 2, false},
	}
	for _, c := range cases {
		if got := isChangedUnit(c.kind, c.depth); got != c.want {
			t.Errorf("isChangedUnit(%q, %d) = %v, want %v", c.kind, c.depth, got, c.want)
		}
	}
}

func TestAggregateReferencesSumsAcrossSeeds(t *testing.T) {
	_, dbPath := newPhase2Repo(t)
	db := func(string) string { return dbPath }

	one, refErr := aggregateReferences([]string{"helper"}, index.ResolveScopeFamily, db)
	if refErr {
		t.Fatalf("unexpected reference error")
	}
	if one.Rows == 0 {
		t.Fatalf("expected helper to have references, got %+v", one)
	}

	both, _ := aggregateReferences([]string{"helper", "Shared"}, index.ResolveScopeFamily, db)
	if both.Rows <= one.Rows {
		t.Errorf("aggregate rows for {helper,Shared} (%d) should exceed helper alone (%d)", both.Rows, one.Rows)
	}
	// helper and Shared are both referenced from main.go. Distinct referencing
	// files must dedup to 1 — summing per-symbol file counts would give 2.
	if both.Files != 1 {
		t.Errorf("distinct referencing_files = %d, want 1 (no double-count of shared file)", both.Files)
	}
}

func TestCollectDefinitionsCountsAndGroups(t *testing.T) {
	_, dbPath := newPhase2Repo(t)
	db := func(string) string { return dbPath }

	byName, total, defErr := collectDefinitions([]string{"helper", "Shared"}, db)
	if defErr {
		t.Fatalf("unexpected definition lookup error")
	}
	if total != 2 {
		t.Errorf("combined definition count = %d, want 2", total)
	}
	if len(byName["helper"]) != 1 || len(byName["Shared"]) != 1 {
		t.Errorf("expected one definition each, got %+v", byName)
	}
}

func TestParseBlobSymbolsOKSemantics(t *testing.T) {
	// Valid Go source parses (ok=true) and yields symbols.
	syms, ok := parseBlobSymbols([]byte("package p\n\nfunc Foo() {}\n"), "p.go")
	if !ok {
		t.Fatalf("valid Go source should parse ok")
	}
	found := false
	for _, s := range syms {
		if s.Name == "Foo" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Foo symbol, got %+v", syms)
	}

	// Binary content (NUL byte) is not parseable.
	if _, ok := parseBlobSymbols([]byte("pack\x00age"), "p.go"); ok {
		t.Errorf("binary content should report ok=false")
	}

	// Recognised-but-not-parseable / unsupported extension is not parseable.
	if _, ok := parseBlobSymbols([]byte("hello world"), "notes.txt"); ok {
		t.Errorf("unsupported extension should report ok=false")
	}

	// A successfully parsed file with no symbols is ok=true (distinct from a
	// parse failure) — this is what prevents false deletions on an emptied file.
	if _, ok := parseBlobSymbols([]byte("package p\n"), "p.go"); !ok {
		t.Errorf("empty-but-valid Go file should report ok=true")
	}
}

func sortedInts(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
