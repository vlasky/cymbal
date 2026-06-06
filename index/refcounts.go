package index

import "strings"

// ReferenceCounts is a complete, name-scoped tally of references to a symbol
// name, split by the production/test/unknown class of the file each reference
// lives in.
//
// "Complete" means these are SQL aggregates with no result limit — unlike the
// BFS caller count (capped by --limit), they are never truncated, so they are
// exact and reproducible.
//
// "Name-scoped" means they count every reference whose name matches within the
// resolution scope, regardless of which same-named definition it targets —
// cymbal resolves references by name, not by type. For a uniquely-named symbol
// that is exactly the set of references to it; for a colliding name (common in
// method-heavy OO codebases) it conflates the same-named definitions. They are
// therefore exact counts of name references, not of references resolved to one
// definition. A reference row is a single call/use site; one enclosing function
// may contain several, so Rows is generally >= the distinct-caller count.
type ReferenceCounts struct {
	Rows  int `json:"reference_rows"`
	Files int `json:"referencing_files"`

	ProductionRows int `json:"production_reference_rows"`
	TestRows       int `json:"test_reference_rows"`
	UnknownRows    int `json:"unknown_reference_rows"`

	ProductionFiles int `json:"production_referencing_files"`
	TestFiles       int `json:"test_referencing_files"`
	UnknownFiles    int `json:"unknown_referencing_files"`
}

// ReferenceCountsWithScope counts indexed references to symbolName within the
// resolution scope derived from the symbol's indexed language(s) — the same
// family-expansion impact/trace use. With ResolveScopeAll (or when the symbol
// has no indexed language) it counts across all languages.
func ReferenceCountsWithScope(dbPath, symbolName string, scope ResolveScope) (ReferenceCounts, error) {
	store, err := openCached(dbPath)
	if err != nil {
		return ReferenceCounts{}, err
	}
	var langs []string
	if NormalizeScope(scope) != ResolveScopeAll {
		if seedLangs, err := store.SymbolLanguages(symbolName); err == nil {
			langs = scopeLanguagesUnion(seedLangs, scope)
		}
	}
	return store.referenceCounts(symbolName, langs)
}

// referenceCounts groups reference rows by file (so the row scan is bounded by
// referencing-file count, not total reference count), then classifies each file
// path and tallies rows and distinct files per class.
func (s *Store) referenceCounts(name string, langs []string) (ReferenceCounts, error) {
	langFilter := ""
	args := []interface{}{name}
	if len(langs) > 0 {
		placeholders := strings.Repeat("?,", len(langs))
		placeholders = placeholders[:len(placeholders)-1]
		langFilter = " AND r.language IN (" + placeholders + ")"
		for _, l := range langs {
			args = append(args, l)
		}
	}

	rows, err := s.db.Query(`
		SELECT f.rel_path, COUNT(*) AS c
		FROM refs r JOIN files f ON r.file_id = f.id
		WHERE r.name = ?`+langFilter+`
		GROUP BY f.rel_path`, args...)
	if err != nil {
		return ReferenceCounts{}, err
	}
	defer rows.Close()

	var rc ReferenceCounts
	for rows.Next() {
		var relPath string
		var c int
		if err := rows.Scan(&relPath, &c); err != nil {
			continue
		}
		rc.Rows += c
		rc.Files++
		switch ClassifyPath(relPath) {
		case PathClassTest:
			rc.TestRows += c
			rc.TestFiles++
		case PathClassUnknown:
			rc.UnknownRows += c
			rc.UnknownFiles++
		default:
			rc.ProductionRows += c
			rc.ProductionFiles++
		}
	}
	return rc, rows.Err()
}
