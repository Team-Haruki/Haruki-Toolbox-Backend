package redis

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestGetClearCachePaths(t *testing.T) {
	t.Parallel()

	paths := GetClearCachePaths("jp", "suite", 123)
	if len(paths) != 1 {
		t.Fatalf("GetClearCachePaths len = %d, want 1", len(paths))
	}
	if paths[0].Namespace != publicAccessNamespace {
		t.Fatalf("first namespace = %q, want %q", paths[0].Namespace, publicAccessNamespace)
	}
	if paths[0].Path != "/public/jp/suite/123" {
		t.Fatalf("first path = %q, want %q", paths[0].Path, "/public/jp/suite/123")
	}
	if paths[0].QueryString != "" {
		t.Fatalf("query string should be empty, got %q", paths[0].QueryString)
	}
}

func TestGetQueryHash(t *testing.T) {
	t.Parallel()

	if got := getQueryHash(""); got != emptyQueryHash {
		t.Fatalf("getQueryHash(empty) = %q, want %q", got, emptyQueryHash)
	}
	if got := getQueryHash("key=upload_time"); got != "b6715d065478a9abd37d540714b8b78d" {
		t.Fatalf("getQueryHash(value) = %q, want fixed md5 hash", got)
	}
}

func TestBuildCacheKey(t *testing.T) {
	t.Parallel()

	if got := buildCacheKey("ns", "/a/b", ""); got != "ns:/a/b:query=none" {
		t.Fatalf("buildCacheKey without query = %q", got)
	}
	if got := buildCacheKey("ns", "/a/b", "x=1"); got != "ns:/a/b:query=a255512f9d61a6777bd5a304235bd26d" {
		t.Fatalf("buildCacheKey with query = %q", got)
	}
}

func TestCacheKeyBuilder(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	var got string
	app.Get("/public/jp/suite/:id", func(c fiber.Ctx) error {
		got = CacheKeyBuilder(c, publicAccessNamespace)
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/public/jp/suite/123?key=upload_time", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test error: %v", err)
	}
	_ = resp.Body.Close()

	want := "public_access:/public/jp/suite/123:query=b6715d065478a9abd37d540714b8b78d"
	if got != want {
		t.Fatalf("CacheKeyBuilder = %q, want %q", got, want)
	}
}
