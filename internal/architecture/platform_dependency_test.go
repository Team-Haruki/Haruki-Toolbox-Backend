package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const platformImportPath = "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform"

// Infrastructure and protocol packages must not depend on application services.
func TestUtilsDoNotImportPlatform(t *testing.T) {
	violations := collectUtilsPlatformImports(t, repositoryRoot(t))
	if len(violations) > 0 {
		t.Fatalf("utils must not import internal/platform; move application services to the platform layer:\n%s", strings.Join(violations, "\n"))
	}
}

func collectUtilsPlatformImports(t *testing.T, repositoryRoot string) []string {
	t.Helper()
	utilsRoot := filepath.Join(repositoryRoot, "utils")
	var edges []string
	err := filepath.WalkDir(utilsRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != utilsRoot && (entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(importPath, platformImportPath+"/") {
				continue
			}
			edges = append(edges, filepath.ToSlash(relative)+" -> "+strings.TrimPrefix(importPath, "github.com/Team-Haruki/Haruki-Toolbox-Backend/"))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("collect utils -> platform imports: %v", err)
	}
	sort.Strings(edges)
	return edges
}
