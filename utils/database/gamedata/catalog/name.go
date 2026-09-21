package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// MaxIdentLen is PostgreSQL's NAMEDATALEN-1. A longer identifier is silently
// TRUNCATED by the server rather than rejected, which would quietly collapse two
// distinct game keys onto one column, so every generated name is fitted to it.
const MaxIdentLen = 63

// SnakeCase converts a Project Sekai camelCase top-level key into a snake_case
// PostgreSQL identifier body (no prefix, no storage suffix).
//
// Rule: insert '_' before an uppercase letter when
//   - the previous rune is lowercase, or
//   - the previous rune is uppercase and the next is lowercase (acronym tail), or
//   - the previous rune is a digit and the next is lowercase.
//
// Worked examples from the real production key set:
//
//	userMusicResults               -> user_music_results
//	userCostume3dStatuses          -> user_costume3d_statuses
//	userCharacterMissionV2Statuses -> user_character_mission_v2_statuses
//	compactUserMusicAchievements   -> compact_user_music_achievements
func SnakeCase(key string) string {
	r := []rune(key)
	var b strings.Builder
	b.Grow(len(key) + 8)
	for i, c := range r {
		if unicode.IsUpper(c) && i > 0 {
			prev := r[i-1]
			var next rune
			if i+1 < len(r) {
				next = r[i+1]
			}
			switch {
			case unicode.IsLower(prev):
				b.WriteByte('_')
			case unicode.IsUpper(prev) && unicode.IsLower(next):
				b.WriteByte('_')
			case unicode.IsDigit(prev) && unicode.IsLower(next):
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(c))
			continue
		}
		switch {
		case unicode.IsUpper(c):
			b.WriteRune(unicode.ToLower(c))
		case unicode.IsLower(c) || unicode.IsDigit(c):
			b.WriteRune(c)
		default:
			// Anything a Sekai key should never contain. Stay deterministic
			// rather than emit an identifier that needs quoting to be legal.
			b.WriteByte('_')
		}
	}
	out := b.String()
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	out = strings.Trim(out, "_")
	if out == "" {
		out = "k"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "k_" + out
	}
	return out
}

// FitIdent truncates body so prefix+body+suffix fits MaxIdentLen, appending a
// short content hash when truncation happens so two long keys sharing a prefix
// cannot collapse onto the same column.
func FitIdent(prefix, body, suffix, hashOf string) string {
	full := prefix + body + suffix
	if len(full) <= MaxIdentLen {
		return full
	}
	sum := sha256.Sum256([]byte(hashOf))
	tag := "_" + hex.EncodeToString(sum[:])[:6]
	budget := MaxIdentLen - len(prefix) - len(suffix) - len(tag)
	if budget < 1 {
		budget = 1
	}
	if budget > len(body) {
		budget = len(body)
	}
	return prefix + body[:budget] + tag + suffix
}
