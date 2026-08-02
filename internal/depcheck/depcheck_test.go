// Package depcheck enforces the architecture rules with a test, so a
// violation is a red build, not a code-review hope:
//
//  1. core/* and ports never import adapters (hexagonal dependency rule).
//  2. core/* and ports never import effectful stdlib packages — no
//     networking, no filesystem, no wall clock, no ambient randomness.
//     All effects must arrive through injected interfaces; that is what
//     makes sim runs deterministic and replayable by seed.
//  3. No cmd/ entry point constructs the credit-Gated registry
//     (registry.NewGated): it hard-requires a durable Publisher and has no
//     token path, so it is sim/test-only and must never back a persistent
//     network (#99, M0 privacy). The production registry is the chain.
//
// Test files are exempt: tests may use os, math/rand, etc.
package depcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

var forbiddenExact = map[string]string{
	"os":           "filesystem access must come through a ChunkStore or other port",
	"time":         "wall-clock time must come through the Clock port",
	"math/rand":    "randomness must be injected (io.Reader or seeded source)",
	"math/rand/v2": "randomness must be injected (io.Reader or seeded source)",
	"crypto/rand":  "randomness must be injected so sims are deterministic",
	"net":          "core has no networking; that's what adapters are for",
}

var forbiddenPrefixes = map[string]string{
	"github.com/nerolabs/silt/adapters/": "core must not import adapters (hexagonal rule)",
	"net/":                               "core has no networking; that's what adapters are for",
	"os/":                                "filesystem access must come through a port",
}

func TestCoreImportsNoAdaptersAndNoEffects(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"core", "ports"} {
		root := filepath.Join(repoRoot, dir)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				pkg := strings.Trim(imp.Path.Value, `"`)
				rel, _ := filepath.Rel(repoRoot, path)
				if why, bad := forbiddenExact[pkg]; bad {
					t.Errorf("%s imports %q — %s", rel, pkg, why)
				}
				for prefix, why := range forbiddenPrefixes {
					if strings.HasPrefix(pkg, prefix) {
						t.Errorf("%s imports %q — %s", rel, pkg, why)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestGatedRegistryFencedOffFromProduction fails the build if any cmd/ file
// references registry.NewGated. The credit-Gated registry records a durable
// Publisher on every entry (no token path), so it is the non-M0, sim/test
// path and must never be wired into a persistent daemon (#99).
func TestGatedRegistryFencedOffFromProduction(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmdRoot := filepath.Join(repoRoot, "cmd")
	err = filepath.WalkDir(cmdRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repoRoot, path)
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "registry" && sel.Sel.Name == "NewGated" {
				t.Errorf("%s constructs registry.NewGated — the credit-Gated registry is sim/test-only (records a durable Publisher, no token path); a persistent daemon must use the chain (#99)", rel)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
