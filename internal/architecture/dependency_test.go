package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulesImportPath = "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules"

func TestProductionPackagesDoNotImportModules(t *testing.T) {
	repositoryRoot := repositoryRoot(t)

	for _, testCase := range []struct {
		name string
		root string
	}{
		{name: "utils", root: "utils"},
		{name: "internal_platform", root: "internal/platform"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := filepath.Join(repositoryRoot, filepath.FromSlash(testCase.root))
			violations := forbiddenModuleImports(t, repositoryRoot, root)
			if len(violations) > 0 {
				t.Fatalf(
					"production code under %s must not import %s:\n%s",
					testCase.root,
					modulesImportPath,
					strings.Join(violations, "\n"),
				)
			}
		})
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func forbiddenModuleImports(t *testing.T, repositoryRoot, root string) []string {
	t.Helper()
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if importPath != modulesImportPath && !strings.HasPrefix(importPath, modulesImportPath+"/") {
				continue
			}
			relativePath, err := filepath.Rel(repositoryRoot, path)
			if err != nil {
				return err
			}
			position := fileSet.Position(imported.Pos())
			violations = append(violations, filepath.ToSlash(relativePath)+":"+strconv.Itoa(position.Line)+" imports "+importPath)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect production imports under %s: %v", root, err)
	}
	sort.Strings(violations)
	return violations
}
