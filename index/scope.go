package index

import "github.com/1broseidon/cymbal/lang"

// ResolveScope controls how name-based traversal (trace/impact) decides which
// indexed symbols a name may resolve to, given the resolution is name-only
// (cymbal stores refs without receiver/type/import context).
//
// The default is family: it admits legitimate cross-language interop
// (JVM java/kotlin/scala, JS javascript/typescript/tsx, C c/cpp) while still
// rejecting unrelated cross-language name collisions.
type ResolveScope string

const (
	// ResolveScopeSame restricts resolution to the seed/caller's exact language.
	ResolveScopeSame ResolveScope = "same"
	// ResolveScopeFamily restricts resolution to the seed/caller's interop
	// family (see lang.Registry.Family). This is the default.
	ResolveScopeFamily ResolveScope = "family"
	// ResolveScopeAll applies no language restriction.
	ResolveScopeAll ResolveScope = "all"
)

// NormalizeScope maps empty or unrecognized values to the family default.
func NormalizeScope(s ResolveScope) ResolveScope {
	switch s {
	case ResolveScopeSame, ResolveScopeAll:
		return s
	default:
		return ResolveScopeFamily
	}
}

// scopeLanguages returns the set of languages a name may resolve to, given the
// seed/caller language and scope. A nil result means "no restriction" (resolve
// across all languages) — used for ResolveScopeAll and for an empty/unknown
// seed language where scoping can't be applied.
func scopeLanguages(seedLang string, scope ResolveScope) []string {
	if seedLang == "" {
		return nil
	}
	switch NormalizeScope(scope) {
	case ResolveScopeAll:
		return nil
	case ResolveScopeSame:
		return []string{seedLang}
	default: // family
		return lang.Default.Family(seedLang)
	}
}

// scopeLanguagesUnion returns the union of per-language scope sets for a set of
// seed languages (used by impact, whose seed name may span languages). A nil
// result means "no restriction": empty input, or any seed that can't be scoped.
func scopeLanguagesUnion(seedLangs []string, scope ResolveScope) []string {
	if len(seedLangs) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, sl := range seedLangs {
		langs := scopeLanguages(sl, scope)
		if langs == nil {
			// An unscopable seed widens the whole query to unrestricted.
			return nil
		}
		for _, l := range langs {
			if _, ok := seen[l]; ok {
				continue
			}
			seen[l] = struct{}{}
			out = append(out, l)
		}
	}
	return out
}

// inLangs reports whether language l is in the langs set. An empty/nil set
// means "no restriction" and matches everything.
func inLangs(l string, langs []string) bool {
	if len(langs) == 0 {
		return true
	}
	for _, x := range langs {
		if x == l {
			return true
		}
	}
	return false
}
