package cmd

import (
	"path/filepath"

	"github.com/1broseidon/cymbal/index"
)

// findSymbolEntry locates the DB in plan.Federated that contains a symbol
// resolvable as `name`. The current cwd's entry is tried first (per
// resolveDBs ordering), so existing single-DB behavior is preserved when
// the symbol exists in cwd's repo.
//
// Returns (entry, true) when the symbol resolves in some DB; otherwise
// returns the primary entry and false so callers can report the
// missing-symbol error against a real DB.
func findSymbolEntry(plan DBPlan, name string) (DBEntry, bool) {
	for _, e := range plan.Federated {
		res, err := flexResolve(e.Path, name)
		if err == nil && len(res.Results) > 0 {
			return e, true
		}
	}
	if len(plan.Federated) > 0 {
		return plan.Federated[0], false
	}
	return DBEntry{Path: plan.Primary, IsCurrent: true}, false
}

// pickDBForFilePath routes a path argument to the DB whose worktree owns
// the file. When the file is inside a federation sibling, that sibling's
// label is returned; when it's outside the federation set, we compute a
// fresh per-repo DB path (option A path-aware routing).
//
// Falls back to (plan.Primary, "") when the path can't be associated with
// any git repo — preserves current behavior for non-repo paths.
func pickDBForFilePath(plan DBPlan, filePath string) (string, string) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return plan.Primary, ""
	}
	root := repoRootForPath(abs)
	if root == "" {
		return plan.Primary, ""
	}
	canonRoot := canonicalForCompare(root)
	for _, e := range plan.Federated {
		if canonicalForCompare(e.Root) == canonRoot {
			return e.Path, e.Label()
		}
	}
	// Path is outside the federation set entirely — route by repo root.
	if dbPath, dbErr := index.RepoDBPath(root); dbErr == nil {
		return dbPath, ""
	}
	return plan.Primary, ""
}

// attachWorktreeLabel stamps a "worktree" key onto a show payload regardless
// of whether the underlying shape is a single map (showAll=false) or a
// slice of maps (showAll=true). Other shapes are returned untouched so
// callers stay forward-compatible.
func attachWorktreeLabel(payload any, label string) any {
	switch p := payload.(type) {
	case map[string]any:
		p["worktree"] = label
		return p
	case []map[string]any:
		for i := range p {
			p[i]["worktree"] = label
		}
		return p
	case []any:
		for i := range p {
			if m, ok := p[i].(map[string]any); ok {
				m["worktree"] = label
				p[i] = m
			}
		}
		return p
	}
	return payload
}

// canonicalForCompare normalizes a path for equality comparison: absolute
// + symlinks resolved + cleaned. Mirrors pathsEqual but returns the
// canonical form so callers can use it as a map key or for repeated
// comparisons.
func canonicalForCompare(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Clean(p)
}
