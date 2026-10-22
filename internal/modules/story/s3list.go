package story

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// emptyPayloadSHA256 is the SHA-256 of an empty body (every GET here).
const emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// s3Lister lists objects of one bucket over the S3 API with path-style URLs
// and AWS Signature V4 (Garage speaks this with region "garage").
type s3Lister struct {
	endpoint  string // scheme://host[:port], no trailing slash
	region    string
	bucket    string
	accessKey string
	secretKey string
	client    *http.Client
	now       func() time.Time
}

type listResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	Contents              []struct{ Key string }
	IsTruncated           bool
	NextContinuationToken string
}

// listAll returns every key under prefix.
func (l *s3Lister) listAll(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	token := ""
	for {
		q := url.Values{}
		q.Set("list-type", "2")
		q.Set("prefix", prefix)
		q.Set("max-keys", "1000")
		if token != "" {
			q.Set("continuation-token", token)
		}
		var res listResult
		if err := l.get(ctx, "/"+l.bucket, q, &res); err != nil {
			return nil, err
		}
		for _, c := range res.Contents {
			keys = append(keys, c.Key)
		}
		if !res.IsTruncated || res.NextContinuationToken == "" {
			return keys, nil
		}
		token = res.NextContinuationToken
	}
}

func (l *s3Lister) get(ctx context.Context, path string, query url.Values, out any) error {
	u, err := url.Parse(l.endpoint + path)
	if err != nil {
		return err
	}
	u.RawQuery = canonicalQuery(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	l.sign(req, u)
	resp, err := l.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("s3 list %s: HTTP %d", path, resp.StatusCode)
	}
	return xml.Unmarshal(body, out)
}

// sign adds the SigV4 headers for a bodiless request.
func (l *s3Lister) sign(req *http.Request, u *url.URL) {
	t := l.now().UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", emptyPayloadSHA256)
	host := u.Host
	canonicalHeaders := "host:" + host + "\n" +
		"x-amz-content-sha256:" + emptyPayloadSHA256 + "\n" +
		"x-amz-date:" + amzDate + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := strings.Join([]string{
		req.Method,
		uriEncodePath(u.Path),
		u.RawQuery,
		canonicalHeaders,
		signedHeaders,
		emptyPayloadSHA256,
	}, "\n")
	scope := day + "/" + l.region + "/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex(canonicalRequest)
	key := hmacSHA256([]byte("AWS4"+l.secretKey), day)
	key = hmacSHA256(key, l.region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+l.accessKey+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

// canonicalQuery sorts keys and percent-encodes per SigV4 (RFC 3986, %20).
func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

func uriEncodePath(p string) string {
	if p == "" {
		return "/"
	}
	return uriEncode(p, false)
}

// uriEncode encodes everything but unreserved characters; "/" survives in paths.
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}
