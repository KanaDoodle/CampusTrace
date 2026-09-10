package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRepairPublicDependencyBoundary(t *testing.T) {
	root := filepath.Join("..", "..")
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(mod), "module github.com/KanaDoodle/CampusTrace\n") {
		t.Fatal("not an independent module")
	}
	if strings.Contains(string(mod), "replace ") {
		t.Fatal("release go.mod contains development replace")
	}
	found := false
	err = filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if e.Name() == ".git" || e.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			imp, ok := n.(*ast.ImportSpec)
			if !ok {
				return true
			}
			v, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(v, "github.com/KanaDoodle/KanaRPC-Go/internal/") {
				t.Errorf("F01: forbidden dependency in %s: %s", path, v)
			}
			if v == "github.com/KanaDoodle/KanaRPC-Go/rpc" {
				found = true
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("real facade import absent")
	}
}
