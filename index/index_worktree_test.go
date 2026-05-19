package index

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// Pure parser tests — no git invocation needed. These pin the porcelain
// parsing contract independently of the local git version.

func TestParseWorktreePorcelainMainAndOne(t *testing.T) {
	in := []byte("worktree /repo/main\nHEAD abc123\nbranch refs/heads/main\n\n" +
		"worktree /repo/wt-foo\nHEAD def456\nbranch refs/heads/feat/foo\n\n")
	got := parseWorktreePorcelain(in)
	want := []WorktreeInfo{
		{Path: "/repo/main", Branch: "main"},
		{Path: "/repo/wt-foo", Branch: "feat/foo"},
	}
	// EvalSymlinks may rewrite /repo/* to itself; compare path basenames to
	// stay robust on systems where /repo/* doesn't exist.
	if len(got) != len(want) {
		t.Fatalf("entry count: got %d want %d (%+v)", len(got), len(want), got)
	}
	for i := range got {
		if filepath.Base(got[i].Path) != filepath.Base(want[i].Path) {
			t.Errorf("entry %d path basename: got %q want %q", i, got[i].Path, want[i].Path)
		}
		if got[i].Branch != want[i].Branch {
			t.Errorf("entry %d branch: got %q want %q", i, got[i].Branch, want[i].Branch)
		}
	}
}

func TestParseWorktreePorcelainDetached(t *testing.T) {
	in := []byte("worktree /repo/main\nHEAD abc123\ndetached\n\n")
	got := parseWorktreePorcelain(in)
	if len(got) != 1 {
		t.Fatalf("entry count: got %d want 1", len(got))
	}
	if got[0].Branch != "" {
		t.Errorf("detached entry should have empty Branch; got %q", got[0].Branch)
	}
}

func TestParseWorktreePorcelainBare(t *testing.T) {
	in := []byte("worktree /repo/bare\nbare\n\n" +
		"worktree /repo/wt-foo\nHEAD def456\nbranch refs/heads/feat/foo\n\n")
	got := parseWorktreePorcelain(in)
	if len(got) != 2 {
		t.Fatalf("entry count: got %d want 2", len(got))
	}
	if !got[0].IsBare {
		t.Errorf("first entry should be IsBare=true; got %+v", got[0])
	}
	if got[1].IsBare {
		t.Errorf("second entry should be IsBare=false; got %+v", got[1])
	}
}

func TestParseWorktreePorcelainEmpty(t *testing.T) {
	if got := parseWorktreePorcelain(nil); len(got) != 0 {
		t.Errorf("empty input must yield zero entries; got %+v", got)
	}
}

func TestParseWorktreePorcelainNoTrailingBlankLine(t *testing.T) {
	// Some git versions omit the trailing blank line on the last entry.
	in := []byte("worktree /repo/main\nbranch refs/heads/main\n")
	got := parseWorktreePorcelain(in)
	if len(got) != 1 || got[0].Branch != "main" {
		t.Errorf("expected one main entry; got %+v", got)
	}
}

func TestParseWorktreePorcelainTolerantOfUnknownKeys(t *testing.T) {
	// Porcelain is additive — future git could add new keys. We must not
	// blow up or misparse on them.
	in := []byte("worktree /repo/main\nHEAD abc\nbranch refs/heads/main\nfuture-thing whatever\n\n")
	got := parseWorktreePorcelain(in)
	if len(got) != 1 || got[0].Branch != "main" {
		t.Errorf("unknown-key tolerance broken; got %+v", got)
	}
}

// Integration tests against a real git binary — skipped automatically when
// git is unavailable so the suite stays portable.

func TestRepoCommonDirMainRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	got, err := RepoCommonDir(dir)
	if err != nil {
		t.Fatalf("RepoCommonDir: %v", err)
	}
	// EvalSymlinks rewrites both sides; compare basenames as a robust check.
	if filepath.Base(got) != ".git" {
		t.Errorf("expected common dir to end in .git; got %q", got)
	}
}

func TestRepoCommonDirWorktreeMatchesMain(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	main := t.TempDir()
	wtRoot := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", main}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := exec.Command("sh", "-c", "echo hi > "+filepath.Join(main, "a.txt")).Run(); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	// Need a commit before worktree add will accept a new branch.
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")
	wtPath := filepath.Join(wtRoot, "wt-feat")
	run("worktree", "add", "-b", "feat", wtPath)

	mainCommon, err := RepoCommonDir(main)
	if err != nil {
		t.Fatalf("main RepoCommonDir: %v", err)
	}
	wtCommon, err := RepoCommonDir(wtPath)
	if err != nil {
		t.Fatalf("worktree RepoCommonDir: %v", err)
	}
	// Both must point at the same place — that's the whole signal.
	if mainCommon != wtCommon {
		t.Errorf("common dir mismatch — federation detection would fail.\n  main:     %s\n  worktree: %s", mainCommon, wtCommon)
	}
}

func TestEnumerateWorktreesReturnsMainAndLinked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	main := t.TempDir()
	wtRoot := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", main}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := exec.Command("sh", "-c", "echo hi > "+filepath.Join(main, "a.txt")).Run(); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")
	wtPath := filepath.Join(wtRoot, "wt-feat")
	run("worktree", "add", "-b", "feat", wtPath)

	commonDir, err := RepoCommonDir(main)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := EnumerateWorktrees(commonDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 worktree entries (main + linked), got %d: %+v", len(entries), entries)
	}
	branches := []string{entries[0].Branch, entries[1].Branch}
	wantSet := map[string]bool{"main": true, "feat": true}
	for _, b := range branches {
		if !wantSet[b] {
			t.Errorf("unexpected branch %q in entries (want main, feat); got %+v", b, entries)
		}
	}
}

func TestRepoCommonDirOutsideRepoReturnsEmpty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir() // no git init
	got, err := RepoCommonDir(dir)
	if err != nil {
		t.Errorf("expected nil error when path isn't in a repo; got %v", err)
	}
	if got != "" {
		t.Errorf("expected empty common dir outside repo; got %q", got)
	}
}

func TestEnumerateWorktreesEmptyCommonDirYieldsNil(t *testing.T) {
	got, err := EnumerateWorktrees("")
	if err != nil {
		t.Errorf("empty commonDir should not error; got %v", err)
	}
	if !reflect.DeepEqual(got, []WorktreeInfo(nil)) {
		t.Errorf("expected nil slice for empty commonDir; got %+v", got)
	}
}
