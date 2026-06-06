package cmd

import (
	"reflect"
	"sort"
	"testing"

	"github.com/1broseidon/cymbal/index"
)

func TestParseChangedFilesAttributesAddedAndDeletedLines(t *testing.T) {
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
`
	touched, deleted := parseChangedFiles(diff)

	got := sortedInts(touched["foo.go"])
	want := []int{11, 12, 21} // 11,12 added; 21 deletion anchor
	if !reflect.DeepEqual(got, want) {
		t.Errorf("foo.go touched lines = %v, want %v", got, want)
	}

	if len(deleted) != 1 || deleted[0] != "gone.go" {
		t.Errorf("deleted = %v, want [gone.go]", deleted)
	}
	if _, ok := touched["gone.go"]; ok {
		t.Errorf("deleted file should not appear in touched set")
	}
}

func TestInnermostChangedSkipsLocalsPrefersInnerDefinition(t *testing.T) {
	syms := []index.SymbolResult{
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
		s    index.SymbolResult
		want bool
	}{
		{index.SymbolResult{Kind: "function", Depth: 0}, true},
		{index.SymbolResult{Kind: "method", Depth: 1}, true},
		{index.SymbolResult{Kind: "variable", Depth: 0}, true},  // top-level var
		{index.SymbolResult{Kind: "variable", Depth: 1}, false}, // local var
		{index.SymbolResult{Kind: "type", Depth: 1}, false},     // function-local type
		{index.SymbolResult{Kind: "type", Depth: 0}, true},
		{index.SymbolResult{Kind: "constant", Depth: 2}, false},
	}
	for _, c := range cases {
		if got := isChangedUnit(c.s); got != c.want {
			t.Errorf("isChangedUnit(%+v) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestAggregateReferencesSumsAcrossSeeds(t *testing.T) {
	_, dbPath := newPhase2Repo(t)
	db := func(string) string { return dbPath }

	// helper is called from multiple sites; Shared from one. Aggregating both
	// seeds should sum their reference rows and definition counts.
	one, defsOne := aggregateReferences([]string{"helper"}, index.ResolveScopeFamily, db)
	if one.Rows == 0 {
		t.Fatalf("expected helper to have references, got %+v", one)
	}
	if defsOne != 1 {
		t.Errorf("helper definition_count = %d, want 1", defsOne)
	}

	both, defsBoth := aggregateReferences([]string{"helper", "Shared"}, index.ResolveScopeFamily, db)
	if both.Rows <= one.Rows {
		t.Errorf("aggregate rows for {helper,Shared} (%d) should exceed helper alone (%d)", both.Rows, one.Rows)
	}
	if defsBoth != 2 {
		t.Errorf("combined definition_count = %d, want 2", defsBoth)
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
