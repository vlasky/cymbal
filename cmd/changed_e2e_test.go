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
	if r.Base != "HEAD" {
		t.Errorf("base = %q, want HEAD", r.Base)
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
