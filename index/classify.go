package index

import "strings"

// PathClass categorises a file by its repo-relative path for triage: is a
// caller production code, test code, or something in between?
//
// Classification is path-pattern based and deliberately conservative. Patterns
// are anchored at directory boundaries (`/tests/`) or filename suffixes
// (`_test.go`, `FooTest.java`), never a bare "test"/"spec" substring — a
// substring rule mis-classifies real production code such as C#
// `*Specification.cs`, Rails `tests_controller.rb`, or a Java `Latest.java`.
// Genuinely ambiguous locations (`/testing/`, `/fixtures/`, `/examples/`) are
// reported as Unknown rather than guessed; only confidently-recognised test
// files are Test. Everything else is Production.
//
// Validated against 8 real repos across 7 languages (Go, Python, Ruby, Java,
// C#, PHP, JS/TS); see CYMBALIMPROVEMENTS-SPEC.md "Empirical sweep".
type PathClass string

const (
	PathClassProduction PathClass = "production"
	PathClassTest       PathClass = "test"
	PathClassUnknown    PathClass = "unknown"
)

// testDirSegments are directory markers that confidently indicate test code.
// Matched as "/seg/" anywhere in the path, or as a leading "seg/" prefix
// (top-level test dirs that have no leading slash in a rel path).
var testDirSegments = []string{
	"test", "tests", "spec", "specs", "__tests__", "testdata", "e2e",
}

// ambiguousDirSegments are markers for code that is neither the production
// surface nor the test suite proper (helpers, fixtures, examples). Reported as
// Unknown so `--no-tests` keeps them rather than dropping real code.
var ambiguousDirSegments = []string{
	"testing", "testutil", "testutils", "fixture", "fixtures",
	"mock", "mocks", "example", "examples", "sample", "samples",
	"demo", "demos",
}

// lowerTestSuffixes are filename suffixes (case-insensitive) that indicate a
// test file in languages whose test convention is lowercase.
var lowerTestSuffixes = []string{
	"_test.go", "_test.py", "_test.rb", "_test.exs", "_test.ex",
	"_spec.rb",
	".test.ts", ".test.tsx", ".test.js", ".test.jsx", ".test.mjs",
	".spec.ts", ".spec.tsx", ".spec.js", ".spec.jsx", ".spec.mjs",
}

// camelTestSuffixes are CamelCase class-filename conventions (Java/Kotlin/C#/
// Scala). Checked against the original-case basename so "Latest.java" or
// "Manifest.cs" do not false-match.
var camelTestSuffixes = []string{
	"Test.java", "Tests.java", "IT.java", "ITCase.java",
	"Test.kt", "Tests.kt",
	"Test.cs", "Tests.cs",
	"Test.scala", "Spec.scala",
}

// ClassifyPath returns the PathClass for a repo-relative file path. The index
// stores rel paths with "/" separators; callers should pass those directly.
func ClassifyPath(relPath string) PathClass {
	if relPath == "" {
		return PathClassUnknown
	}
	p := strings.ToLower(relPath)
	base := p
	baseOrig := relPath
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		base = p[i+1:]
		baseOrig = relPath[i+1:]
	}

	// Confident test directory markers.
	for _, seg := range testDirSegments {
		if strings.Contains(p, "/"+seg+"/") || strings.HasPrefix(p, seg+"/") {
			return PathClassTest
		}
	}

	// Confident test filename markers.
	for _, suf := range lowerTestSuffixes {
		if strings.HasSuffix(base, suf) {
			return PathClassTest
		}
	}
	if base == "conftest.py" {
		return PathClassTest
	}
	if (strings.HasPrefix(base, "test_")) &&
		(strings.HasSuffix(base, ".py") || strings.HasSuffix(base, ".rb")) {
		return PathClassTest
	}
	for _, suf := range camelTestSuffixes {
		// Require a real prefix before the convention so "Test.java" alone
		// (no class name) doesn't count, and lowercase "latest.java" can't match.
		if strings.HasSuffix(baseOrig, suf) && len(baseOrig) > len(suf) {
			return PathClassTest
		}
	}

	// Ambiguous (helpers/fixtures/examples) → Unknown.
	for _, seg := range ambiguousDirSegments {
		if strings.Contains(p, "/"+seg+"/") || strings.HasPrefix(p, seg+"/") {
			return PathClassUnknown
		}
	}

	return PathClassProduction
}
