package cmd

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/1broseidon/cymbal/index"
)

func gitChanged(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// TestChangedStagedVsUnstagedSeparation proves the two-blob fix: a staged edit
// and an unrelated unstaged edit in the same file are attributed to the right
// mode (default = unstaged only; --staged = staged only).
func TestChangedStagedVsUnstagedSeparation(t *testing.T) {
	t.Setenv("CYMBAL_CACHE_DIR", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "go.mod", "module example.com/sep\n\ngo 1.25\n")
	writeFile(t, repo, "p.go", "package p\n\nfunc Alpha() int { return 1 }\n\nfunc Gamma() int { return 3 }\n")
	gitChanged(t, repo, "init")
	gitChanged(t, repo, "add", ".")
	gitChanged(t, repo, "-c", "user.name=x", "-c", "user.email=a@b.c", "commit", "-m", "init")
	if _, err := index.Index(repo, "", index.Options{Workers: 1, Force: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(index.CloseAll)

	// Stage an edit to Alpha; leave an unrelated edit to Gamma unstaged.
	writeFile(t, repo, "p.go", "package p\n\nfunc Alpha() int { return 111 }\n\nfunc Gamma() int { return 3 }\n")
	gitChanged(t, repo, "add", "p.go")
	writeFile(t, repo, "p.go", "package p\n\nfunc Alpha() int { return 111 }\n\nfunc Gamma() int { return 333 }\n")

	run := func(args ...string) string {
		var out string
		withWorkingDir(t, repo, func() {
			s, _, err := captureProcessOutput(t, func() error {
				rootCmd.SetArgs(args)
				defer func() {
					rootCmd.SetArgs(nil)
					// cobra retains parsed flag values across Execute calls;
					// reset what we set so later tests aren't polluted.
					_ = changedCmd.Flags().Set("staged", "false")
					_ = changedCmd.Flags().Set("base", "")
					_ = rootCmd.PersistentFlags().Set("json", "false")
				}()
				return rootCmd.Execute()
			})
			if err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			out = s
		})
		return out
	}

	if def := run("changed"); !strings.Contains(def, "# Gamma") || strings.Contains(def, "# Alpha") {
		t.Errorf("default (unstaged) should report Gamma only, got:\n%s", def)
	}
	if st := run("changed", "--staged"); !strings.Contains(st, "# Alpha") || strings.Contains(st, "# Gamma") {
		t.Errorf("--staged should report Alpha only, got:\n%s", st)
	}
}

// TestChangedNamesDeletedSymbols proves the old-side attribution fix: deleting a
// whole function names it under deleted_symbols, rather than mis-attributing the
// deletion to a surviving neighbour.
func TestChangedNamesDeletedSymbols(t *testing.T) {
	t.Setenv("CYMBAL_CACHE_DIR", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "go.mod", "module example.com/del\n\ngo 1.25\n")
	writeFile(t, repo, "p.go", "package p\n\nfunc Alpha() int { return 1 }\n\nfunc Beta() int { return 2 }\n\nfunc Gamma() int { return 3 }\n")
	gitChanged(t, repo, "init")
	gitChanged(t, repo, "add", ".")
	gitChanged(t, repo, "-c", "user.name=x", "-c", "user.email=a@b.c", "commit", "-m", "init")
	if _, err := index.Index(repo, "", index.Options{Workers: 1, Force: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(index.CloseAll)

	// Delete Beta entirely (unstaged), leaving Alpha and Gamma.
	writeFile(t, repo, "p.go", "package p\n\nfunc Alpha() int { return 1 }\n\nfunc Gamma() int { return 3 }\n")

	var stdout string
	withWorkingDir(t, repo, func() {
		out, _, err := captureProcessOutput(t, func() error {
			rootCmd.SetArgs([]string{"changed", "--json"})
			defer func() {
				rootCmd.SetArgs(nil)
				_ = rootCmd.PersistentFlags().Set("json", "false")
			}()
			return rootCmd.Execute()
		})
		if err != nil {
			t.Fatalf("changed --json: %v", err)
		}
		stdout = out
	})

	var env struct {
		Results struct {
			Deleted []struct {
				Symbol string `json:"symbol"`
			} `json:"deleted_symbols"`
			Results []struct {
				Symbol string `json:"symbol"`
			} `json:"results"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("parse changed json: %v\n%s", err, stdout)
	}
	found := false
	for _, d := range env.Results.Deleted {
		if d.Symbol == "Beta" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Beta under deleted_symbols, got: %s", stdout)
	}
	// Beta must NOT be mis-reported as a changed (modified) symbol.
	for _, r := range env.Results.Results {
		if r.Symbol == "Beta" {
			t.Errorf("deleted Beta should not appear as a changed symbol")
		}
	}
}

// TestChangedEndToEnd exercises the full pipeline: real git diff -> changed-line
// attribution -> per-symbol references + impact, via the CLI.
func TestChangedEndToEnd(t *testing.T) {
	t.Setenv("CYMBAL_CACHE_DIR", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "go.mod", "module example.com/ch\n\ngo 1.25\n")
	writeFile(t, repo, "app.go", `package app

func target() {}

func Caller() {
	target()
}
`)
	gitChanged(t, repo, "init")
	gitChanged(t, repo, "add", ".")
	gitChanged(t, repo, "-c", "user.name=Cymbal Test", "-c", "user.email=cymbal@example.invalid", "commit", "-m", "initial")

	if _, err := index.Index(repo, "", index.Options{Workers: 1, Force: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(index.CloseAll)

	// Modify Caller's body so the working tree differs from HEAD.
	writeFile(t, repo, "app.go", `package app

func target() {}

func Caller() {
	target()
	target()
}
`)

	var stdout string
	withWorkingDir(t, repo, func() {
		out, _, err := captureProcessOutput(t, func() error {
			rootCmd.SetArgs([]string{"changed", "--json"})
			defer func() {
				rootCmd.SetArgs(nil)
				// --json is a persistent flag on rootCmd; cobra retains its
				// parsed value across Execute calls, so reset it or later
				// tests inherit JSON output.
				_ = rootCmd.PersistentFlags().Set("json", "false")
			}()
			return rootCmd.Execute()
		})
		if err != nil {
			t.Fatalf("changed --json: %v", err)
		}
		stdout = out
	})

	// Output is wrapped by the root --json envelope: {"results": <payload>, ...}.
	var env struct {
		Results struct {
			ChangedSymbols int    `json:"changed_symbols"`
			Base           string `json:"base"`
			Results        []struct {
				Symbol     string `json:"symbol"`
				References struct {
					Rows int `json:"reference_rows"`
				} `json:"references"`
				Impact *struct {
					TotalCallers int `json:"total_callers"`
				} `json:"impact"`
			} `json:"results"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("parse changed json: %v\n%s", err, stdout)
	}
	r := env.Results
	if r.Base != "working tree" {
		t.Errorf("base = %q, want \"working tree\" (default unstaged mode)", r.Base)
	}
	var caller *struct {
		Symbol     string `json:"symbol"`
		References struct {
			Rows int `json:"reference_rows"`
		} `json:"references"`
		Impact *struct {
			TotalCallers int `json:"total_callers"`
		} `json:"impact"`
	}
	for i := range r.Results {
		if r.Results[i].Symbol == "Caller" {
			caller = &r.Results[i]
		}
	}
	if caller == nil {
		t.Fatalf("changed did not report Caller as changed: %s", stdout)
	}
	// Caller now calls target() twice; references are name-scoped counts of the
	// 'Caller' name itself (it has none here), so just assert impact wired up.
	if caller.Impact == nil {
		t.Errorf("Caller missing impact summary")
	}
}
