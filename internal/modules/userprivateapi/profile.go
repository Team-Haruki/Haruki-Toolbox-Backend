package userprivateapi

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
)

// queryPrivateProfile selects a server-defined view of the private suite.
const queryPrivateProfile = "profile"

// privateProfile is a named, server-defined view of the private suite
// document: the whole document minus a server-side deny list.
//
// It exists instead of a client key list for three reasons:
//
//   - A whole-document render keeps `extra` (keys this build has no column for,
//     which is where a new game version's keys land), omits keys the row does
//     not carry instead of answering null, and filters userGamedata to its
//     seven served fields. A key list does none of these.
//   - One canonical body per document generation can be cached with the full
//     body's 7-day horizon. Arbitrary ?key= permutations stay on the 6-hour cap
//     that stops a caller from minting a week of junk entries.
//   - The deny list changes on the server, without a client release.
type privateProfile struct {
	name string
	// omit lists the top-level suite keys the profile never serves.
	omit []string
	// surface is the cache-key surface segment. It starts with "private" and
	// carries a digest of the deny list, so a deploy that changes the list
	// moves readers to new entries instead of serving bodies cached under the
	// old one for up to a week. It never contains ':', so it stays one segment
	// of the cache key. Profile bodies are written through the same indexed
	// cache write as full bodies (SetGameDataBodyCache), so an upload's
	// ClearCache drops them exactly like full bodies.
	surface string
}

// privateProfiles is the server-side registry. Keep every list here short and
// limited to keys the profile's consumer provably never reads.
var privateProfiles = map[string]*privateProfile{
	// Haruki Cloud never reads the two costume3d catalogues, which are 23%
	// (CN/TW) to 47% (JP) of a compressed suite body and more than half of the
	// raw JSON Cloud decodes.
	"cloud": newPrivateProfile("cloud", "userCostume3dStatuses", "userCostume3dShopItems"),
}

func newPrivateProfile(name string, omit ...string) *privateProfile {
	sum := sha256.Sum256([]byte(strings.Join(omit, ",")))
	return &privateProfile{
		name:    name,
		omit:    omit,
		surface: "private-profile-" + name + "-" + hex.EncodeToString(sum[:4]),
	}
}

// resolvePrivateProfile validates the profile query against the rest of the
// request. An empty name means no profile. It only inspects the query, so it
// runs before any database work and leaks nothing about the account.
func resolvePrivateProfile(name, requestKey string, dataType harukiUtils.UploadDataType) (*privateProfile, string) {
	if name == "" {
		return nil, ""
	}
	p, ok := privateProfiles[name]
	if !ok {
		return nil, "invalid profile"
	}
	if dataType != harukiUtils.UploadDataTypeSuite {
		return nil, "profile is only supported for suite"
	}
	if requestKey != "" {
		return nil, "profile cannot be combined with key"
	}
	return p, ""
}

// cacheSurface is the cache-key surface for a request: the profile's own, or
// without a profile the private surface privateCacheSurface picks for the
// request's ?key= filter. A profile is never combined with a key.
func (p *privateProfile) cacheSurface(requestKey string) string {
	if p == nil {
		return privateCacheSurface(requestKey)
	}
	return p.surface
}

// flightSuffix keeps a profile's singleflight group apart from the full
// body's and from every ?key= filter.
func (p *privateProfile) flightSuffix() string {
	if p == nil {
		return ""
	}
	return "\x00profile=" + p.name
}
