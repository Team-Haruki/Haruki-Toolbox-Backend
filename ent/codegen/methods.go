// Package codegen maintains project-specific Ent output adaptations. Applying
// them as generation hooks makes regeneration reproducible.
package codegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"entgo.io/ent/entc/gen"
)

// Modernize selects JSON v2 and moves the generic interceptor helper into its owning type.
// Ent does not yet emit Go 1.27 generic methods itself.
func Modernize(next gen.Generator) gen.Generator {
	return gen.GenerateFunc(func(graph *gen.Graph) error {
		if err := next.Generate(graph); err != nil {
			return err
		}
		return filepath.WalkDir(graph.Target, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, source, parser.ParseComments)
			if err != nil {
				return err
			}
			RewriteJSON(file)
			owners := map[string]bool{}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Type.TypeParams == nil {
					continue
				}
				owner := genericOwner(fn.Name.Name)
				if owner == "" {
					return fmt.Errorf("unmapped Ent generic function %s in %s", fn.Name.Name, path)
				}
				fn.Recv = &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent(owner)}}}
				owners[owner] = true
			}
			for _, owner := range []string{"queryInterceptorRunner", "mutationHookRunner"} {
				if owners[owner] {
					file.Decls = append(file.Decls, &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{Name: ast.NewIdent(owner), Type: &ast.StructType{Fields: &ast.FieldList{}}}}})
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.CallExpr:
					node.Fun = genericMethod(node.Fun)
				case *ast.IndexExpr:
					node.X = genericMethod(node.X)
				case *ast.IndexListExpr:
					node.X = genericMethod(node.X)
				}
				return true
			})
			var output bytes.Buffer
			if err := format.Node(&output, fset, file); err != nil {
				return err
			}
			return os.WriteFile(path, output.Bytes(), 0644)
		})
	})
}

func genericOwner(name string) string {
	switch name {
	case "withHooks":
		return "mutationHookRunner"
	case "withInterceptors", "scanWithInterceptors", "querierAll", "querierCount":
		return "queryInterceptorRunner"
	default:
		return ""
	}
}

func genericMethod(expr ast.Expr) ast.Expr {
	id, ok := expr.(*ast.Ident)
	if !ok {
		return expr
	}
	owner := genericOwner(id.Name)
	if owner == "" {
		return expr
	}
	return &ast.SelectorExpr{X: &ast.CompositeLit{Type: ast.NewIdent(owner)}, Sel: ast.NewIdent(id.Name)}
}

// RewriteJSON selects the v2 API and preserves v1 omission of zero scalars and
// nil pointers via explicit omitzero. Empty collections still use omitempty.
func RewriteJSON(file *ast.File) {
	for _, spec := range file.Imports {
		if spec.Path.Value == `"encoding/json"` {
			spec.Path.Value = `"encoding/json/v2"`
			spec.Name = ast.NewIdent("json")
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		field, ok := node.(*ast.Field)
		if !ok || field.Tag == nil {
			return true
		}
		zero := false
		switch typ := field.Type.(type) {
		case *ast.StarExpr, *ast.InterfaceType:
			zero = true
		case *ast.Ident:
			switch typ.Name {
			case "bool", "any", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "float32", "float64":
				zero = true
			}
		}
		if zero {
			tag, err := strconv.Unquote(field.Tag.Value)
			if err == nil {
				// Only replace the JSON tag; other serializers have their own semantics.
				start := strings.Index(tag, `json:"`)
				if start >= 0 {
					end := strings.Index(tag[start+6:], `"`)
					if end >= 0 {
						end += start + 6
						tag = tag[:start] + strings.ReplaceAll(tag[start:end], ",omitempty", ",omitzero") + tag[end:]
						field.Tag.Value = "`" + tag + "`"
					}
				}
			}
		}
		return true
	})
}
