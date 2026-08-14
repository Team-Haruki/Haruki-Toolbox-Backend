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

// utils still contains legacy adapters that consume a few shared platform
// capabilities. Freeze those edges while the layers are disentangled: files
// may stop importing platform packages, but no new file or dependency may be
// added without an explicit architecture decision.
var legacyUtilsPlatformImports = []string{
	"utils/api/helper.go -> internal/platform/runtimeconfig",
	"utils/api/session_auth_proxy.go -> internal/platform/identity",
	"utils/api/session_kratos_admin.go -> internal/platform/identity",
	"utils/api/session_kratos_client.go -> internal/platform/identity",
	"utils/api/session_kratos_identity.go -> internal/platform/identity",
	"utils/api/session_profile_sync.go -> internal/platform/identity",
	"utils/api/session_verify.go -> internal/platform/authheader",
	"utils/oauth2/middleware.go -> internal/platform/authheader",
}

func TestUtilsDoNotAddPlatformDependencies(t *testing.T) {
	if !sort.StringsAreSorted(legacyUtilsPlatformImports) {
		t.Fatal("legacy utils -> platform baseline must be sorted")
	}
	for index := 1; index < len(legacyUtilsPlatformImports); index++ {
		if legacyUtilsPlatformImports[index] == legacyUtilsPlatformImports[index-1] {
			t.Fatalf("duplicate utils -> platform baseline %q", legacyUtilsPlatformImports[index])
		}
	}

	root := repositoryRoot(t)
	actual := collectUtilsPlatformImports(t, root)
	want := append([]string(nil), legacyUtilsPlatformImports...)
	if strings.Join(actual, "\n") == strings.Join(want, "\n") {
		return
	}

	actualSet := make(map[string]struct{}, len(actual))
	wantSet := make(map[string]struct{}, len(want))
	for _, edge := range actual {
		actualSet[edge] = struct{}{}
	}
	for _, edge := range want {
		wantSet[edge] = struct{}{}
	}
	var added, stale []string
	for _, edge := range actual {
		if _, ok := wantSet[edge]; !ok {
			added = append(added, edge)
		}
	}
	for _, edge := range want {
		if _, ok := actualSet[edge]; !ok {
			stale = append(stale, edge)
		}
	}
	t.Fatalf(
		"utils -> internal/platform dependency baseline drifted\nadded (move the capability or inject a narrow port):\n%s\nstale (shrink the baseline):\n%s",
		strings.Join(added, "\n"),
		strings.Join(stale, "\n"),
	)
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
