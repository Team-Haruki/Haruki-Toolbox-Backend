package redis

import "testing"

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

func TestBuildGameDataCacheKey(t *testing.T) {
	t.Parallel()

	got := BuildGameDataCacheKey("public", "jp", "suite", 123, " upload_time ")
	want := "game_data:public:jp:suite:123:query=b6715d065478a9abd37d540714b8b78d"
	if got != want {
		t.Fatalf("BuildGameDataCacheKey = %q, want %q", got, want)
	}

	if got := BuildGameDataCacheKey("private", "jp", "mysekai", 123, ""); got != "game_data:private:jp:mysekai:123:query=none" {
		t.Fatalf("BuildGameDataCacheKey empty query = %q", got)
	}

	keyAC := BuildGameDataCacheKey("oauth2", "jp", "suite", 123, "a,c")
	keyCA := BuildGameDataCacheKey("oauth2", "jp", "suite", 123, "c,a")
	if keyAC == keyCA {
		t.Fatalf("different key order should produce different cache keys, both = %q", keyAC)
	}
}
