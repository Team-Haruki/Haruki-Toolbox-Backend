package codegen

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"entgo.io/ent/entc/gen"
)

// entFixture mirrors the shape of Ent's generated client.go: package-level generic
// helpers plus call sites that use them directly or through explicit instantiation.
const entFixture = `package ent

import (
	"context"
	"encoding/json"
)

type Payload struct {
	Count int ` + "`json:\"count,omitempty\"`" + `
}

func withHooks[V any, M any](ctx context.Context, exec func(context.Context) (V, error), mutation M) (V, error) {
	return exec(ctx)
}

func querierAll[V any, Q any]() V {
	var zero V
	return zero
}

func scanWithInterceptors[Q any, V any](ctx context.Context, q Q, v V) error {
	return nil
}

func plain() {}

func (Payload) Save(ctx context.Context) (int, error) {
	_ = querierAll[int, Payload]
	_ = scanWithInterceptors(ctx, Payload{}, 0)
	plain()
	_, _ = json.Marshal(Payload{})
	return withHooks(ctx, func(context.Context) (int, error) { return 1, nil }, Payload{})
}
`

func generateWith(t *testing.T, target string, files map[string]string) error {
	t.Helper()
	next := gen.GenerateFunc(func(graph *gen.Graph) error {
		for name, content := range files {
			path := filepath.Join(graph.Target, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
		}
		return nil
	})
	return Modernize(next).Generate(&gen.Graph{Config: &gen.Config{Target: target}})
}

func TestModernizeMovesGenericHelpersOntoOwnerTypes(t *testing.T) {
	target := t.TempDir()
	notes := "withHooks[V any]() is not Go source"
	if err := generateWith(t, target, map[string]string{
		"client.go":        entFixture,
		"nested/schema.go": "package nested\n\nfunc keep() {}\n",
		"README.txt":       notes,
	}); err != nil {
		t.Fatalf("Modernize: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(target, "client.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		`json "encoding/json/v2"`,
		`json:"count,omitzero"`,
		"func (mutationHookRunner) withHooks[V any, M any](",
		"func (queryInterceptorRunner) querierAll[V any, Q any]() V",
		"func (queryInterceptorRunner) scanWithInterceptors[Q any, V any](",
		"type queryInterceptorRunner struct {",
		"type mutationHookRunner struct {",
		"queryInterceptorRunner{}.querierAll[int, Payload]",
		"queryInterceptorRunner{}.scanWithInterceptors(ctx, Payload{}, 0)",
		"mutationHookRunner{}.withHooks(ctx,",
		"\tplain()\n",
		"func plain() {}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rewritten client.go missing %q:\n%s", want, got)
		}
	}

	nested, err := os.ReadFile(filepath.Join(target, "nested", "schema.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(nested) != "package nested\n\nfunc keep() {}\n" {
		t.Errorf("file without generic helpers changed: %q", nested)
	}
	readme, err := os.ReadFile(filepath.Join(target, "README.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(readme) != notes {
		t.Errorf("non-Go file was rewritten: %q", readme)
	}
}

func TestModernizeOnlyDeclaresOwnersThatAreUsed(t *testing.T) {
	target := t.TempDir()
	src := "package ent\n\nfunc withInterceptors[Q any](q Q) Q { return q }\n"
	if err := generateWith(t, target, map[string]string{"query.go": src}); err != nil {
		t.Fatalf("Modernize: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(target, "query.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "type queryInterceptorRunner struct {") {
		t.Errorf("missing queryInterceptorRunner type:\n%s", out)
	}
	if strings.Contains(string(out), "mutationHookRunner") {
		t.Errorf("unused mutationHookRunner declared:\n%s", out)
	}
}

func TestModernizeRejectsUnmappedGenericFunction(t *testing.T) {
	target := t.TempDir()
	src := "package ent\n\nfunc mystery[T any](v T) T { return v }\n"
	err := generateWith(t, target, map[string]string{"mystery.go": src})
	if err == nil || !strings.Contains(err.Error(), "unmapped Ent generic function mystery") {
		t.Fatalf("expected unmapped generic error, got %v", err)
	}
	out, readErr := os.ReadFile(filepath.Join(target, "mystery.go"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(out) != src {
		t.Errorf("file must be left untouched on error, got %q", out)
	}
}

func TestModernizePropagatesParseErrors(t *testing.T) {
	target := t.TempDir()
	err := generateWith(t, target, map[string]string{"broken.go": "package ent\nfunc {"})
	if err == nil || !strings.Contains(err.Error(), "broken.go") {
		t.Fatalf("expected parse error naming broken.go, got %v", err)
	}
}

func TestModernizePropagatesGeneratorError(t *testing.T) {
	sentinel := errors.New("generate failed")
	next := gen.GenerateFunc(func(*gen.Graph) error { return sentinel })
	target := t.TempDir()
	path := filepath.Join(target, "client.go")
	if err := os.WriteFile(path, []byte(entFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Modernize(next).Generate(&gen.Graph{Config: &gen.Config{Target: target}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected generator error, got %v", err)
	}
	out, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(out) != entFixture {
		t.Error("files must not be rewritten when the wrapped generator fails")
	}
}

func TestModernizeReportsMissingTarget(t *testing.T) {
	next := gen.GenerateFunc(func(*gen.Graph) error { return nil })
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	err := Modernize(next).Generate(&gen.Graph{Config: &gen.Config{Target: missing}})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected not-exist error, got %v", err)
	}
}
