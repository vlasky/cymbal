package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1broseidon/cymbal/index"
	"github.com/spf13/cobra"
)

// worktreeFixture builds the reporter's repro: a main repo plus one linked
// worktree, each with a distinct symbol. Returns absolute paths.
func worktreeFixture(t *testing.T) (mainRepo, worktreeRepo string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	defer index.CloseAll()

	cacheDir := t.TempDir()
	t.Setenv("CYMBAL_CACHE_DIR", cacheDir)

	mainRepo = t.TempDir()
	writeFile(t, mainRepo, "sudoku.ts", "export class Sudoku {}\n")
	runGit(t, mainRepo, "init", "-q", "-b", "main")
	runGit(t, mainRepo, "add", ".")
	runGit(t, mainRepo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")

	wtParent := t.TempDir()
	worktreeRepo = filepath.Join(wtParent, "wt-validator")
	runGit(t, mainRepo, "worktree", "add", "-b", "feat/validator", worktreeRepo)
	writeFile(t, worktreeRepo, "validator.ts", "export class SudokuValidator {}\n")
	runGit(t, worktreeRepo, "add", ".")
	runGit(t, worktreeRepo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "add-validator")

	if _, err := index.Index(mainRepo, "", index.Options{Workers: 1, Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Index(worktreeRepo, "", index.Options{Workers: 1, Force: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(index.CloseAll)
	return mainRepo, worktreeRepo
}

// newPlanTestCmd builds a cobra.Command with the persistent flags resolveDBs
// reads, so unit tests can drive the function without going through rootCmd.
func newPlanTestCmd(t *testing.T, flagValues map[string]string) *cobra.Command {
	t.Helper()
	c := &cobra.Command{Use: "test"}
	// Use local Flags() so values set here are visible to resolveDBs without
	// going through Execute() (which is when cobra normally materializes
	// persistent-flag inheritance).
	c.Flags().StringP("db", "d", "", "")
	c.Flags().Bool("json", false, "")
	c.Flags().Bool("no-federate", false, "")
	for name, val := range flagValues {
		if err := c.Flags().Set(name, val); err != nil {
			t.Fatalf("flag set %s=%s: %v", name, val, err)
		}
	}
	return c
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}

// TestWorktreeFederationSearchFromMainCwd is the reporter's repro: from the
// main repo cwd, search for a symbol that exists only in the worktree. The
// federation layer must find it and label it with the worktree.
func TestWorktreeFederationSearchFromMainCwd(t *testing.T) {
	mainRepo, _ := worktreeFixture(t)
	chdir(t, mainRepo)

	cmd := newPlanTestCmd(t, nil)
	plan := resolveDBs(cmd)
	if len(plan.Federated) != 2 {
		t.Fatalf("expected 2 federated DBs (main + worktree); got %d: %+v", len(plan.Federated), plan.Federated)
	}
	entry, found := findSymbolEntry(plan, "SudokuValidator")
	if !found {
		t.Fatalf("federation must find SudokuValidator across worktrees; entry=%+v", entry)
	}
	if entry.IsCurrent {
		t.Errorf("SudokuValidator lives in the worktree, not cwd's repo; got IsCurrent=true")
	}
	if entry.Label() == "" {
		t.Errorf("non-current entry must produce a worktree label; got empty")
	}
}

// TestWorktreeFederationNonGoalCrossGraph enforces non-goal #1: a symbol
// resolved in a sibling worktree must NOT trigger graph traversal into the
// current cwd's DB. The seed locator returns the worktree entry; downstream
// callers (impact/trace/refs) run against that single DB only.
func TestWorktreeFederationNonGoalCrossGraph(t *testing.T) {
	mainRepo, worktreeRepo := worktreeFixture(t)
	chdir(t, mainRepo)

	cmd := newPlanTestCmd(t, nil)
	plan := resolveDBs(cmd)
	entry, found := findSymbolEntry(plan, "SudokuValidator")
	if !found {
		t.Fatalf("seed must resolve in worktree DB")
	}
	// The chosen DB must be the worktree's, not main's.
	wantDB, err := index.RepoDBPath(worktreeRepo)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Path != wantDB {
		t.Errorf("seed routed to wrong DB.\n  want: %s\n   got: %s", wantDB, entry.Path)
	}
	// Sanity: querying main's DB for the worktree-only symbol must return
	// empty (the graph would otherwise leak across worktrees).
	mainDB, err := index.RepoDBPath(mainRepo)
	if err != nil {
		t.Fatal(err)
	}
	res, err := index.SearchSymbols(mainDB, index.SearchQuery{Text: "SudokuValidator", Exact: true, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Errorf("main DB must not contain worktree-only symbol; got %+v", res)
	}
}

// TestWorktreeNoFederationFlag asserts that --no-federate restores
// pre-federation single-DB behavior even when sibling worktrees exist.
func TestWorktreeNoFederationFlag(t *testing.T) {
	mainRepo, _ := worktreeFixture(t)
	chdir(t, mainRepo)

	cmd := newPlanTestCmd(t, map[string]string{"no-federate": "true"})
	plan := resolveDBs(cmd)
	if !plan.NoFederate {
		t.Errorf("expected NoFederate=true; got %+v", plan)
	}
	if len(plan.Federated) != 1 {
		t.Errorf("--no-federate must collapse to 1 DB; got %d: %+v", len(plan.Federated), plan.Federated)
	}
	if !plan.Federated[0].IsCurrent {
		t.Errorf("the single entry must be the current cwd; got %+v", plan.Federated[0])
	}
}

// TestWorktreeNoFederationWithDBOverride asserts hard guarantee #3:
// --db / CYMBAL_DB suppresses federation even with worktrees present.
func TestWorktreeNoFederationWithDBOverride(t *testing.T) {
	mainRepo, _ := worktreeFixture(t)
	chdir(t, mainRepo)

	mainDB, err := index.RepoDBPath(mainRepo)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("--db flag", func(t *testing.T) {
		cmd := newPlanTestCmd(t, map[string]string{"db": mainDB})
		plan := resolveDBs(cmd)
		if !plan.NoFederate {
			t.Errorf("--db must disable federation; got %+v", plan)
		}
		if len(plan.Federated) != 1 || plan.Federated[0].Path != mainDB {
			t.Errorf("--db must produce single entry pointing at the override; got %+v", plan.Federated)
		}
	})

	t.Run("CYMBAL_DB env", func(t *testing.T) {
		t.Setenv("CYMBAL_DB", mainDB)
		cmd := newPlanTestCmd(t, nil)
		plan := resolveDBs(cmd)
		if !plan.NoFederate {
			t.Errorf("CYMBAL_DB must disable federation; got %+v", plan)
		}
	})
}

// TestWorktreeFederationSkipsUnindexedSiblings asserts non-goal #2: a
// sibling worktree without an indexed DB is silently skipped, with a one-line
// stderr note. We must NOT trigger EnsureFresh on foreign worktrees.
func TestWorktreeFederationSkipsUnindexedSiblings(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	defer index.CloseAll()

	cacheDir := t.TempDir()
	t.Setenv("CYMBAL_CACHE_DIR", cacheDir)

	mainRepo := t.TempDir()
	writeFile(t, mainRepo, "a.ts", "export class A {}\n")
	runGit(t, mainRepo, "init", "-q", "-b", "main")
	runGit(t, mainRepo, "add", ".")
	runGit(t, mainRepo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	wtParent := t.TempDir()
	wt := filepath.Join(wtParent, "unindexed")
	runGit(t, mainRepo, "worktree", "add", "-b", "feat", wt)
	// Index ONLY main; the sibling stays unindexed.
	if _, err := index.Index(mainRepo, "", index.Options{Workers: 1, Force: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(index.CloseAll)

	chdir(t, mainRepo)
	cmd := newPlanTestCmd(t, nil)

	stdout, stderr, err := captureProcessOutput(t, func() error {
		_ = resolveDBs(cmd)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" {
		t.Errorf("resolveDBs must not write to stdout; got %q", stdout)
	}
	if !strings.Contains(stderr, "skipped 1 unindexed sibling worktree") {
		t.Errorf("expected stderr note about unindexed sibling; got %q", stderr)
	}
	// And the resulting plan must contain only the indexed (current) entry.
	plan := resolveDBs(cmd)
	if len(plan.Federated) != 1 || !plan.Federated[0].IsCurrent {
		t.Errorf("plan must drop unindexed siblings; got %+v", plan.Federated)
	}
}

// TestWorktreeNoRegressionInPlainRepo asserts hard guarantee #1: a non-worktree
// repo runs through the federation path with byte-identical results to today
// (single entry, IsCurrent=true, no fan-out cost beyond a single git invocation).
func TestWorktreeNoRegressionInPlainRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	defer index.CloseAll()

	cacheDir := t.TempDir()
	t.Setenv("CYMBAL_CACHE_DIR", cacheDir)

	repo := t.TempDir()
	writeFile(t, repo, "x.ts", "export class X {}\n")
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	if _, err := index.Index(repo, "", index.Options{Workers: 1, Force: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(index.CloseAll)

	chdir(t, repo)
	cmd := newPlanTestCmd(t, nil)
	plan := resolveDBs(cmd)
	if len(plan.Federated) != 1 {
		t.Errorf("plain repo must have exactly 1 federated entry; got %d: %+v", len(plan.Federated), plan.Federated)
	}
	if !plan.Federated[0].IsCurrent {
		t.Errorf("the single entry must be IsCurrent; got %+v", plan.Federated[0])
	}
	if plan.Federated[0].Label() != "" {
		t.Errorf("current entry must produce empty label; got %q", plan.Federated[0].Label())
	}
}

// TestWorktreeFederationCwdHitsSortFirst asserts that result ordering puts
// cwd's hits before sibling worktree hits — important for keeping the most
// relevant matches at the top when symbols collide across worktrees.
func TestWorktreeFederationCwdHitsSortFirst(t *testing.T) {
	mainRepo, worktreeRepo := worktreeFixture(t)
	// Search "Sudoku" from the worktree — it exists in both main (inherited
	// file) and worktree. The worktree's entry should be IsCurrent and come
	// first in plan.Federated.
	chdir(t, worktreeRepo)
	cmd := newPlanTestCmd(t, nil)
	plan := resolveDBs(cmd)
	if len(plan.Federated) < 2 {
		t.Fatalf("expected federation with 2+ entries; got %+v", plan.Federated)
	}
	if !plan.Federated[0].IsCurrent {
		t.Errorf("first entry must be IsCurrent; got %+v", plan.Federated)
	}
	// Sanity: main shows up as a non-current sibling.
	wantMainPath := canonicalForCompare(mainRepo)
	foundMain := false
	for _, e := range plan.Federated[1:] {
		if canonicalForCompare(e.Root) == wantMainPath {
			foundMain = true
		}
	}
	if !foundMain {
		t.Errorf("main repo must appear as a non-current sibling; got %+v", plan.Federated)
	}
}
