package index

import (
	"path/filepath"
	"testing"
)

// newTriageRepo builds a tiny repo where a target function has two production
// callers and one test-file caller, for exercising --no-tests and truncation.
func newTriageRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("CYMBAL_CACHE_DIR", t.TempDir())
	repo := t.TempDir()
	writePhase3File(t, repo, "go.mod", "module example.com/triage\n\ngo 1.25\n")
	writePhase3File(t, repo, "app.go", `package app

func target() {}

func ProdUser() {
	target()
}

func AnotherProd() {
	target()
}
`)
	writePhase3File(t, repo, "app_test.go", `package app

func TestTarget() {
	target()
}
`)
	runPhase3Git(t, repo, "init")
	runPhase3Git(t, repo, "add", ".")
	runPhase3Git(t, repo, "-c", "user.name=Cymbal Test", "-c", "user.email=cymbal@example.invalid", "commit", "-m", "initial")
	if _, err := Index(repo, "", Options{Workers: 1, Force: true}); err != nil {
		t.Fatal(err)
	}
	dbPath, err := RepoDBPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(CloseAll)
	return dbPath
}

func TestFindImpactNoTestsFiltersTestCallers(t *testing.T) {
	db := newTriageRepo(t)

	// Default: all three callers present, not truncated.
	all, truncated, err := FindImpactWithScope(db, "target", ResolveScopeFamily, 2, 50, false)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Errorf("expected not truncated with limit 50, got truncated")
	}
	if !impactContainsCaller(all, "ProdUser") || !impactContainsCaller(all, "AnotherProd") {
		t.Fatalf("default impact missing production callers: %+v", all)
	}
	if !impactContainsCaller(all, "TestTarget") {
		t.Fatalf("default impact should include test caller TestTarget: %+v", all)
	}

	// --no-tests: the test-file caller is dropped, production kept.
	noTests, _, err := FindImpactWithScope(db, "target", ResolveScopeFamily, 2, 50, true)
	if err != nil {
		t.Fatal(err)
	}
	if impactContainsCaller(noTests, "TestTarget") {
		t.Fatalf("--no-tests should exclude TestTarget: %+v", noTests)
	}
	if !impactContainsCaller(noTests, "ProdUser") || !impactContainsCaller(noTests, "AnotherProd") {
		t.Fatalf("--no-tests dropped production callers: %+v", noTests)
	}
}

func TestFindImpactReportsTruncation(t *testing.T) {
	db := newTriageRepo(t)

	rows, truncated, err := FindImpactWithScope(db, "target", ResolveScopeFamily, 2, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Errorf("expected truncated with limit 1 and multiple callers")
	}
	if len(rows) != 1 {
		t.Errorf("expected exactly 1 row at limit 1, got %d", len(rows))
	}
}

func TestClassifyImpactPathOfTestFile(t *testing.T) {
	// Sanity: the test fixture's test file classifies as test.
	if got := ClassifyPath(filepath.ToSlash("app_test.go")); got != PathClassTest {
		t.Errorf("app_test.go classified as %q, want test", got)
	}
}
