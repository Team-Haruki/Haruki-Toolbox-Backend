package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// These compatibility aggregates are migration baselines, not extension
// points. Fields may be removed as consumers receive narrower dependencies,
// but adding another process service here would deepen service-locator usage.
var legacyAggregateFields = map[string][]string{
	"utils/api/helper.go:HarukiToolboxRouterHelpers": {
		"BotCredentialSignToken",
		"BotRegistrationEnabled",
		"DBManager",
		"HarukiProxySecret",
		"HarukiProxyUnpackKey",
		"HarukiProxyUserAgent",
		"HarukiProxyVersion",
		"PrivateAPIToken",
		"PrivateAPIUserAgent",
		"PublicAPIAllowedKeys",
		"Router",
		"RuntimeConfig",
		"SMTPClient",
		"SekaiAPIClient",
		"SessionHandler",
		"WebhookEnabled",
		"WebhookJWTSecret",
		"publicAPIKeysMu",
		"runtimeConfigMu",
	},
	"utils/database/manager.go:HarukiToolboxDBManager": {
		"BotDB",
		"DB",
		"Mongo",
		"Redis",
	},
}

func TestLegacyServiceLocatorAggregatesDoNotGrow(t *testing.T) {
	root := repositoryRoot(t)
	keys := make([]string, 0, len(legacyAggregateFields))
	for key := range legacyAggregateFields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		file, typeName, ok := strings.Cut(key, ":")
		if !ok || file == "" || typeName == "" {
			t.Fatalf("invalid aggregate baseline key %q; want path.go:TypeName", key)
		}
		allowed := legacyAggregateFields[key]
		if !sort.StringsAreSorted(allowed) {
			t.Fatalf("aggregate field baseline for %s must be sorted", key)
		}
		for i := 1; i < len(allowed); i++ {
			if allowed[i] == allowed[i-1] {
				t.Fatalf("aggregate field baseline for %s contains duplicate %q", key, allowed[i])
			}
		}

		actual := namedStructFields(t, filepath.Join(root, filepath.FromSlash(file)), typeName)
		allowedSet := make(map[string]struct{}, len(allowed))
		for _, field := range allowed {
			allowedSet[field] = struct{}{}
		}
		var added []string
		for _, field := range actual {
			if _, ok := allowedSet[field]; !ok {
				added = append(added, field)
			}
		}
		if len(added) > 0 {
			t.Fatalf(
				"%s gained field(s) %s; inject the capability directly instead of expanding the compatibility aggregate",
				key,
				strings.Join(added, ", "),
			)
		}
	}
}

func namedStructFields(t *testing.T, path, typeName string) []string {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			named, ok := specification.(*ast.TypeSpec)
			if !ok || named.Name.Name != typeName {
				continue
			}
			structure, ok := named.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("%s in %s is no longer a struct", typeName, path)
			}
			var fields []string
			for _, field := range structure.Fields.List {
				if len(field.Names) == 0 {
					t.Fatalf("%s in %s gained embedded field %s; inject it directly", typeName, path, fmt.Sprint(field.Type))
				}
				for _, name := range field.Names {
					fields = append(fields, name.Name)
				}
			}
			sort.Strings(fields)
			return fields
		}
	}
	t.Fatalf("type %s not found in %s", typeName, path)
	return nil
}
