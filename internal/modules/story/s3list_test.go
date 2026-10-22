package story

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// AWS SigV4 documentation example "GET Bucket (List Objects)":
// GET /?max-keys=2&prefix=J on examplebucket.s3.amazonaws.com.
func TestSignMatchesAWSExample(t *testing.T) {
	l := &s3Lister{
		region:    "us-east-1",
		accessKey: "AKIAIOSFODNN7EXAMPLE",
		secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		now:       func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) },
	}
	u, _ := url.Parse("https://examplebucket.s3.amazonaws.com/")
	q := url.Values{}
	q.Set("max-keys", "2")
	q.Set("prefix", "J")
	u.RawQuery = canonicalQuery(q)
	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	l.sign(req, u)
	want := "Signature=34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"
	if got := req.Header.Get("Authorization"); !strings.HasSuffix(got, want) {
		t.Fatalf("Authorization = %q, want suffix %q", got, want)
	}
}

func TestCanonicalQueryEncoding(t *testing.T) {
	q := url.Values{}
	q.Set("prefix", "jp-assets/start app/")
	q.Set("list-type", "2")
	if got, want := canonicalQuery(q), "list-type=2&prefix=jp-assets%2Fstart%20app%2F"; got != want {
		t.Fatalf("canonicalQuery = %q, want %q", got, want)
	}
}
