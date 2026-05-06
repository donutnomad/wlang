// Normalize implements LANGUAGE.md §7.2: it rewrites legacy / deprecated AST
// shapes into the current form before Parse runs, and emits deprecation
// diagnostics so callers can show migration hints (TC-604, TC-882, TC-883).
//
// The normalizer operates on the decoded JSON tree (any) so that it can
// transform shapes that the strict Parse stage would otherwise reject. After
// Normalize completes, the tree is re-serialized and handed to ParseProgram.
package compiler

import (
	"encoding/json"

	"github.com/wflang/wflang/ast"
	werr "github.com/wflang/wflang/errors"
)

// Deprecation is one entry in the deprecation table (LANGUAGE.md §13.2).
type Deprecation struct {
	// From is the legacy AST key that triggers migration.
	From string
	// To is the current AST key the legacy shape maps to.
	To string
	// Since is the language version that introduced the deprecation.
	Since string
	// Message is the human-readable migration hint.
	Message string
}

// deprecationTable lists every deprecated legacy form. Adding new entries here
// is the single source of truth for the Normalize migrator and is exposed
// through DeprecationTable() so tools can render migration docs.
var deprecationTable = []Deprecation{
	{
		From:    "loop",
		To:      "fori",
		Since:   "wflang/v1.1",
		Message: `legacy "loop" is deprecated; use "fori" instead`,
	},
	{
		From:    "return_value",
		To:      "return",
		Since:   "wflang/v1.1",
		Message: `legacy "return_value" is deprecated; use "return" instead`,
	},
}

// DeprecationTable returns a copy of the deprecation registry (TC-882).
func DeprecationTable() []Deprecation {
	out := make([]Deprecation, len(deprecationTable))
	copy(out, deprecationTable)
	return out
}

// Normalize decodes raw JSON, rewrites deprecated forms, and re-encodes the
// result. The diagnostics slice records every legacy shape that was migrated
// (TC-604, TC-883). When raw doesn't match any deprecation entry the input is
// returned unchanged with an empty diagnostics list.
func Normalize(raw []byte) ([]byte, []ast.Diagnostic, error) {
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, nil, werr.Newf(werr.CodeJSONDecode,
			"normalize: invalid JSON: %v", err)
	}
	var diags []ast.Diagnostic
	tree = normalizeNode(tree, "", &diags)
	if len(diags) == 0 {
		return raw, nil, nil
	}
	out, err := json.Marshal(tree)
	if err != nil {
		return nil, nil, werr.Newf(werr.CodeJSONDecode,
			"normalize: re-marshal failed: %v", err)
	}
	return out, diags, nil
}

// Migrate is the public migrator (LANGUAGE.md §13.2 / TC-883). Given a legacy
// program it returns the rewritten JSON plus the list of applied deprecations.
// Programs that contain no deprecated shapes round-trip unchanged.
func Migrate(raw []byte) ([]byte, []ast.Diagnostic, error) {
	return Normalize(raw)
}

// normalizeNode walks an arbitrary JSON value, rewriting any keyed map whose
// single key matches a deprecation entry. Each rewrite appends a Diagnostic.
func normalizeNode(n any, path string, diags *[]ast.Diagnostic) any {
	switch x := n.(type) {
	case map[string]any:
		for k, v := range x {
			x[k] = normalizeNode(v, ast.NewPath(path, k), diags)
		}
		// Single-key shape lookup against the deprecation table.
		if len(x) == 1 {
			for _, d := range deprecationTable {
				if v, ok := x[d.From]; ok {
					delete(x, d.From)
					x[d.To] = v
					*diags = append(*diags, ast.Diagnostic{
						Severity: ast.SeverityDeprecation,
						Code:     "W_DEPRECATED",
						Path:     path,
						Message:  d.Message,
					})
					break
				}
			}
		}
		return x
	case []any:
		for i, v := range x {
			x[i] = normalizeNode(v, ast.NewPath(path, itoa(i)), diags)
		}
		return x
	default:
		return n
	}
}

// itoa is a tiny base-10 int→string helper used for JSON Pointer index
// fragments. Avoids dragging strconv into this otherwise-tiny file.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
