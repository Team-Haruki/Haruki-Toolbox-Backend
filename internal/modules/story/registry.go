package story

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// fetcher reads master files from the Haruki registry and scenario JSON from
// the asset host. Master files are cached per region until the registry's
// content hash changes.
type fetcher struct {
	registryBase string
	assetBase    string
	userAgent    string
	client       *http.Client

	mu    sync.Mutex
	hash  map[string]string            // region → contentHash of the cached set
	cache map[string]map[string][]byte // region → file → body
	// localizedOK remembers "<region>/<path>" scenarios found translated;
	// untranslated ones are checked again on the next catalog build.
	localizedOK map[string]bool
}

func newFetcher(registryBase, assetBase, userAgent string, client *http.Client) *fetcher {
	return &fetcher{
		registryBase: strings.TrimRight(registryBase, "/"),
		assetBase:    strings.TrimRight(assetBase, "/"),
		userAgent:    userAgent,
		client:       client,
		hash:         map[string]string{},
		cache:        map[string]map[string][]byte{},
	}
}

func (f *fetcher) getBytes(ctx context.Context, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", f.userAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

// contentHash returns the registry's current hash of a region's master set.
func (f *fetcher) contentHash(ctx context.Context, region string) (string, error) {
	body, _, err := f.getBytes(ctx, f.registryBase+"/v1/master/"+region+"/current")
	if err != nil {
		return "", err
	}
	var v struct {
		ContentHash string `json:"contentHash"`
		DataVersion string `json:"dataVersion"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", err
	}
	if v.ContentHash != "" {
		return v.ContentHash, nil
	}
	return v.DataVersion, nil
}

// master decodes one master file (e.g. "eventStories") of a region into out.
func (f *fetcher) master(ctx context.Context, region, name string, out any) error {
	hash, err := f.contentHash(ctx, region)
	if err != nil {
		return err
	}
	f.mu.Lock()
	if f.hash[region] != hash {
		f.hash[region] = hash
		f.cache[region] = map[string][]byte{}
	}
	body, ok := f.cache[region][name]
	f.mu.Unlock()
	if !ok {
		u := f.registryBase + "/v1/master/" + region + "/files/" + name + ".json?version=" + url.QueryEscape(hash)
		if body, _, err = f.getBytes(ctx, u); err != nil {
			return err
		}
		f.mu.Lock()
		if f.hash[region] == hash {
			f.cache[region][name] = body
		}
		f.mu.Unlock()
	}
	return json.Unmarshal(body, out)
}

// scenario fetches "<region>-assets/<path>"; a 404 returns (nil, nil).
func (f *fetcher) scenario(ctx context.Context, region, assetPath string) (*Scenario, error) {
	body, code, err := f.getBytes(ctx, f.assetBase+"/"+region+"-assets/"+assetPath)
	if code == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Scenario
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// localized reports whether the region's copy of an episode holds translated
// text rather than the JP placeholder. Lookup errors count as not localized.
func (f *fetcher) localized(ctx context.Context, region string, ep *Episode) bool {
	for _, p := range ep.Paths {
		key := region + "/" + p
		f.mu.Lock()
		ok := f.localizedOK[key]
		f.mu.Unlock()
		if ok {
			return true
		}
		s, err := f.scenario(ctx, region, p)
		if err != nil || s == nil {
			continue
		}
		if kanaRatio(s) >= untranslatedKana {
			return false
		}
		f.mu.Lock()
		if f.localizedOK == nil {
			f.localizedOK = map[string]bool{}
		}
		f.localizedOK[key] = true
		f.mu.Unlock()
		return true
	}
	return false
}

// Scenario is the part of ScenarioSceneData the translator reads.
type Scenario struct {
	ScenarioID string `json:"ScenarioId"`
	TalkData   []struct {
		WindowDisplayName string `json:"WindowDisplayName"`
		Body              string `json:"Body"`
		TalkCharacters    []struct {
			Character2dID int `json:"Character2dId"`
		} `json:"TalkCharacters"`
	} `json:"TalkData"`
}
