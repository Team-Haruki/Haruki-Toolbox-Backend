package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const configImportPath = "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"

type globalConfigReference struct {
	file     string
	selector string
}

type globalConfigAllowance struct {
	file     string
	selector string
	count    int
}

// legacyGlobalConfigAllowlist records the global configuration reads that still
// need to be migrated to constructor-injected dependencies. Keep entries sorted
// by file and selector so additions and removals remain easy to review.
var legacyGlobalConfigAllowlist = []globalConfigAllowance{
	{file: "utils/api/ios/rules.go", selector: "config.Cfg.SekaiClient", count: 1},
	{file: "utils/handler/uploader.go", selector: "config.Cfg.ThirdPartyDataProvider", count: 1},
	{file: "utils/sekai/config.go", selector: "config.Cfg.SekaiClient", count: 1},
	{file: "utils/sekai/proxy.go", selector: "config.Cfg.SekaiClient", count: 1},
	{file: "utils/sekai/retriever_types.go", selector: "config.Cfg.Proxy", count: 1},
}

func TestProductionPackagesDoNotAddGlobalConfigReferences(t *testing.T) {
	if !sort.SliceIsSorted(legacyGlobalConfigAllowlist, func(i, j int) bool {
		left := legacyGlobalConfigAllowlist[i]
		right := legacyGlobalConfigAllowlist[j]
		if left.file == right.file {
			return left.selector < right.selector
		}
		return left.file < right.file
	}) {
		t.Fatal("legacy global config allowlist must be sorted by file and selector")
	}

	allowed := make(map[globalConfigReference]int, len(legacyGlobalConfigAllowlist))
	for _, allowance := range legacyGlobalConfigAllowlist {
		if allowance.count < 1 {
			t.Fatalf("global config allowance must have a positive count: %s %s", allowance.file, allowance.selector)
		}
		reference := globalConfigReference{file: allowance.file, selector: allowance.selector}
		if _, exists := allowed[reference]; exists {
			t.Fatalf("duplicate global config allowance: %s %s", allowance.file, allowance.selector)
		}
		allowed[reference] = allowance.count
	}

	repositoryRoot := repositoryRoot(t)
	actual := make(map[globalConfigReference][]int)
	for _, root := range []string{"api", "internal/modules", "internal/platform", "utils"} {
		collectGlobalConfigReferences(t, repositoryRoot, filepath.Join(repositoryRoot, filepath.FromSlash(root)), actual)
	}

	var expanded []string
	for reference, lines := range actual {
		allowedCount, exists := allowed[reference]
		if exists && len(lines) <= allowedCount {
			continue
		}
		if !exists {
			expanded = append(expanded, fmt.Sprintf(
				"%s:%s uses %s (%d occurrence(s))",
				reference.file,
				formatLines(lines),
				reference.selector,
				len(lines),
			))
			continue
		}
		expanded = append(expanded, fmt.Sprintf(
			"%s:%s uses %s %d time(s), allowlist permits %d",
			reference.file,
			formatLines(lines),
			reference.selector,
			len(lines),
			allowedCount,
		))
	}
	sort.Strings(expanded)

	var stale []string
	for _, allowance := range legacyGlobalConfigAllowlist {
		reference := globalConfigReference{file: allowance.file, selector: allowance.selector}
		actualCount := len(actual[reference])
		if actualCount >= allowance.count {
			continue
		}
		if actualCount == 0 {
			stale = append(stale, fmt.Sprintf("remove %s %s", allowance.file, allowance.selector))
			continue
		}
		stale = append(stale, fmt.Sprintf(
			"lower %s %s from %d to %d",
			allowance.file,
			allowance.selector,
			allowance.count,
			actualCount,
		))
	}

	if len(expanded) == 0 && len(stale) == 0 {
		return
	}

	var message strings.Builder
	message.WriteString("global config reference guard drifted")
	if len(expanded) > 0 {
		message.WriteString("\nnew or expanded references (inject configuration instead):\n")
		message.WriteString(strings.Join(expanded, "\n"))
	}
	if len(stale) > 0 {
		message.WriteString("\nstale allowlist entries (shrink the migration baseline):\n")
		message.WriteString(strings.Join(stale, "\n"))
	}
	t.Fatal(message.String())
}

func collectGlobalConfigReferences(
	t *testing.T,
	repositoryRoot string,
	root string,
	references map[globalConfigReference][]int,
) {
	t.Helper()
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
		parsed, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			return err
		}
		relativePath = filepath.ToSlash(relativePath)

		aliases, dotImports := configImportAliases(parsed)
		for _, dotImport := range dotImports {
			reference := globalConfigReference{
				file:     relativePath,
				selector: "config.<dot-import>",
			}
			references[reference] = append(references[reference], fileSet.Position(dotImport.Pos()).Line)
		}
		if len(aliases) == 0 {
			return nil
		}

		var ancestors []ast.Node
		ast.Inspect(parsed, func(node ast.Node) bool {
			if node == nil {
				ancestors = ancestors[:len(ancestors)-1]
				return true
			}

			var parent ast.Node
			if len(ancestors) > 0 {
				parent = ancestors[len(ancestors)-1]
			}
			ancestors = append(ancestors, node)

			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if parentSelector, ok := parent.(*ast.SelectorExpr); ok && parentSelector.X == selector {
				return true
			}

			chain, ok := normalizedConfigSelector(selector, aliases)
			if !ok || len(chain) < 2 || chain[1] != "Cfg" {
				return true
			}
			reference := globalConfigReference{
				file:     relativePath,
				selector: strings.Join(chain, "."),
			}
			references[reference] = append(references[reference], fileSet.Position(selector.Pos()).Line)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("inspect global config references under %s: %v", root, err)
	}
}

func configImportAliases(file *ast.File) (map[string]struct{}, []*ast.ImportSpec) {
	aliases := make(map[string]struct{})
	var dotImports []*ast.ImportSpec
	for _, imported := range file.Imports {
		importPath, err := strconv.Unquote(imported.Path.Value)
		if err != nil || importPath != configImportPath {
			continue
		}
		if imported.Name == nil {
			aliases["config"] = struct{}{}
			continue
		}
		if imported.Name.Name == "." {
			dotImports = append(dotImports, imported)
			continue
		}
		if imported.Name.Name != "_" {
			aliases[imported.Name.Name] = struct{}{}
		}
	}
	return aliases, dotImports
}

func normalizedConfigSelector(expression ast.Expr, aliases map[string]struct{}) ([]string, bool) {
	switch expression := expression.(type) {
	case *ast.Ident:
		if _, ok := aliases[expression.Name]; !ok {
			return nil, false
		}
		return []string{"config"}, true
	case *ast.ParenExpr:
		return normalizedConfigSelector(expression.X, aliases)
	case *ast.SelectorExpr:
		chain, ok := normalizedConfigSelector(expression.X, aliases)
		if !ok {
			return nil, false
		}
		return append(chain, expression.Sel.Name), true
	default:
		return nil, false
	}
}

func formatLines(lines []int) string {
	formatted := make([]string, len(lines))
	for index, line := range lines {
		formatted[index] = strconv.Itoa(line)
	}
	return strings.Join(formatted, ",")
}
