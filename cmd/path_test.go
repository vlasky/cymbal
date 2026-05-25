package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/1broseidon/cymbal/index"
)

func TestPathCommandFindsRoute(t *testing.T) {
	defer index.CloseAll()

	repoDir := t.TempDir()
	src := `package main

func entrypoint() {
	middle()
}

func middle() {
	leaf()
}

func leaf() {}
`
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := index.Index(repoDir, dbPath, index.Options{Workers: 1}); err != nil {
		t.Fatal(err)
	}

	out, err := index.FindPath(dbPath, "entrypoint", "leaf", 5, index.TraceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != index.PathFound {
		t.Fatalf("expected PathFound, got %d", out.Status)
	}
	if len(out.Path) != 2 {
		t.Fatalf("expected 2 hops (entrypoint→middle→leaf), got %d: %+v", len(out.Path), out.Path)
	}
	if out.Path[0].Caller != "entrypoint" || out.Path[0].Callee != "middle" {
		t.Errorf("hop 1: %+v", out.Path[0])
	}
	if out.Path[1].Caller != "middle" || out.Path[1].Callee != "leaf" {
		t.Errorf("hop 2: %+v", out.Path[1])
	}
}

func TestPathCommandNoRoute(t *testing.T) {
	defer index.CloseAll()

	repoDir := t.TempDir()
	src := `package main

func alpha() {}
func beta() {}
`
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := index.Index(repoDir, dbPath, index.Options{Workers: 1}); err != nil {
		t.Fatal(err)
	}

	out, err := index.FindPath(dbPath, "alpha", "beta", 5, index.TraceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != index.PathNotReachable {
		t.Errorf("expected PathNotReachable for disconnected symbols, got %d", out.Status)
	}
}
