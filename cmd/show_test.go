package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1broseidon/cymbal/index"
)

func TestIsFilePathWithSymbolSuffix(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// File paths (no colon or numeric suffix)
		{"internal/store.go", true},
		{"store.go", true},
		{"store.go:80-120", true},
		{"store.go:L80-L120", true},
		// File:Symbol — should NOT be a file path (routes to symbol lookup)
		{"store.go:SearchSymbols", false},
		{"internal/store.go:SearchSymbols", false},
		{"parser.ts:extractRefs", false},
		// Pure symbol names
		{"SearchSymbols", false},
		{"Store.SearchSymbols", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isFilePath(tt.input)
			if got != tt.want {
				t.Errorf("isFilePath(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseSymbolArgFileScoped(t *testing.T) {
	tests := []struct {
		input    string
		wantFile string
		wantSym  string
	}{
		{"store.go:SearchSymbols", "store.go", "SearchSymbols"},
		{"internal/index/store.go:SearchSymbols", "internal/index/store.go", "SearchSymbols"},
		{"parser.ts:extractRefs", "parser.ts", "extractRefs"},
		// Numeric suffix — not a symbol
		{"store.go:80", "", "store.go:80"},
		{"store.go:L80", "", "store.go:L80"},
		// No colon — plain symbol
		{"SearchSymbols", "", "SearchSymbols"},
		// Dot-qualified — not file-scoped
		{"Store.SearchSymbols", "", "Store.SearchSymbols"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			file, sym := parseSymbolArg(tt.input)
			if file != tt.wantFile || sym != tt.wantSym {
				t.Errorf("parseSymbolArg(%q) = (%q, %q), want (%q, %q)",
					tt.input, file, sym, tt.wantFile, tt.wantSym)
			}
		})
	}
}

func TestRepoBoundFilePathRejectsOutsideRepo(t *testing.T) {
	defer index.CloseAll()

	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\nfunc inside() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := index.Index(repoDir, dbPath, index.Options{Workers: 1}); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(t.TempDir(), "secret.go")
	if err := os.WriteFile(outside, []byte("package secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := repoBoundFilePath(dbPath, outside)
	if err == nil || !strings.Contains(err.Error(), "outside repository") {
		t.Fatalf("expected outside-repository error, got %v", err)
	}
}

func TestRepoBoundFilePathRejectsSymlinkEscape(t *testing.T) {
	defer index.CloseAll()

	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\nfunc inside() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := index.Index(repoDir, dbPath, index.Options{Workers: 1}); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(t.TempDir(), "secret.go")
	if err := os.WriteFile(target, []byte("package secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(repoDir, "leak.go")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	_, err := repoBoundFilePath(dbPath, linkPath)
	if err == nil || !strings.Contains(err.Error(), "outside repository") {
		t.Fatalf("expected symlink escape error, got %v", err)
	}
}
