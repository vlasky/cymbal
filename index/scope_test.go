package index

import (
	"sort"
	"testing"
)

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestScopeLanguages(t *testing.T) {
	tests := []struct {
		seed  string
		scope ResolveScope
		want  []string // sorted; nil means unrestricted
	}{
		{"", ResolveScopeFamily, nil}, // empty seed -> unrestricted
		{"go", ResolveScopeAll, nil},  // all -> unrestricted
		{"go", ResolveScopeSame, []string{"go"}},
		{"kotlin", ResolveScopeSame, []string{"kotlin"}},
		{"kotlin", ResolveScopeFamily, []string{"java", "kotlin", "scala"}},
		{"typescript", ResolveScopeFamily, []string{"javascript", "tsx", "typescript"}},
		{"go", ResolveScopeFamily, []string{"go"}},       // no family -> self
		{"cobol", ResolveScopeFamily, []string{"cobol"}}, // unknown -> self
		{"go", "", []string{"go"}},                       // empty scope normalizes to family
	}
	for _, tt := range tests {
		got := scopeLanguages(tt.seed, tt.scope)
		sort.Strings(got)
		if !eqStrs(got, tt.want) {
			t.Errorf("scopeLanguages(%q, %q) = %v, want %v", tt.seed, tt.scope, got, tt.want)
		}
	}
}

func TestScopeLanguagesUnion(t *testing.T) {
	// empty input -> unrestricted
	if got := scopeLanguagesUnion(nil, ResolveScopeFamily); got != nil {
		t.Errorf("empty seed set should be unrestricted, got %v", got)
	}
	// an unscopable seed (empty language) widens the whole query to unrestricted
	if got := scopeLanguagesUnion([]string{"go", ""}, ResolveScopeFamily); got != nil {
		t.Errorf("unscopable seed should widen to unrestricted, got %v", got)
	}
	// union of two families, deduped
	got := scopeLanguagesUnion([]string{"java", "go"}, ResolveScopeFamily)
	sort.Strings(got)
	want := []string{"go", "java", "kotlin", "scala"}
	if !eqStrs(got, want) {
		t.Errorf("union(java,go) = %v, want %v", got, want)
	}
}

func TestInLangs(t *testing.T) {
	if !inLangs("go", nil) {
		t.Error("nil set should match everything")
	}
	if !inLangs("go", []string{"python", "go"}) {
		t.Error("go should be in {python,go}")
	}
	if inLangs("rust", []string{"python", "go"}) {
		t.Error("rust should not be in {python,go}")
	}
}

func TestNormalizeScope(t *testing.T) {
	for _, s := range []ResolveScope{ResolveScopeSame, ResolveScopeAll, ResolveScopeFamily} {
		if NormalizeScope(s) != s {
			t.Errorf("NormalizeScope(%q) should be identity", s)
		}
	}
	if NormalizeScope("") != ResolveScopeFamily {
		t.Error("empty should normalize to family")
	}
	if NormalizeScope("bogus") != ResolveScopeFamily {
		t.Error("unknown should normalize to family")
	}
}
