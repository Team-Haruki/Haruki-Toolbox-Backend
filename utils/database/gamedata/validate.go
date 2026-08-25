package gamedata

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Limits bound how much an upload may store.
//
// These exist because removing MongoDB removes the only upper bound the system
// had on attacker-influenced growth. Mongo refuses a document over 16 MB;
// PostgreSQL accepts 1 GB in a single field and Fiber's BodyLimit is 100 MB, so
// without explicit caps a single upload could store two orders of magnitude more
// than anything the game produces.
//
// Upload payloads are decrypted with the PUBLIC Project Sekai client key, so
// their contents and their key names are attacker-influenced, not merely
// user-supplied.
type Limits struct {
	// MaxKeyBytes caps one key's encoded value. Production's largest observed
	// single key is ~6.5 MB (userCostume3dStatuses, a whole-game catalogue).
	MaxKeyBytes int
	// MaxRowBytes caps the whole upload. Production's largest observed document
	// is 13.52 MB with 21 keys blanked, and ~24 MB with them restored.
	MaxRowBytes int
	// MaxExtraKeys caps how many unknown top-level keys one upload may park in
	// `extra`. A legitimate client sends keys this build does not know only when
	// the game ships new ones — a handful at a time, not thousands.
	MaxExtraKeys int
	// MaxExtraBytes caps the encoded size of the whole `extra` column.
	MaxExtraBytes int
}

// DefaultLimits are sized from the measured production distribution with room
// for the game to grow, not from what PostgreSQL happens to tolerate.
func DefaultLimits() Limits {
	return Limits{
		MaxKeyBytes:   64 << 20,  // 64 MiB: ~10x the largest observed key
		MaxRowBytes:   192 << 20, // 192 MiB: ~8x the largest observed document
		MaxExtraKeys:  128,
		MaxExtraBytes: 32 << 20,
	}
}

// ValidateUploadFieldNames rejects field names an upload must never carry.
//
// It keeps the MongoDB-era rejection of `.` and `$` even though PostgreSQL has
// no such operators: those names round-trip through the Mongo path during the
// cutover, and a name that is legal in one store and an operator in the other is
// exactly the kind of difference that turns into an injection later.
//
// It additionally rejects NUL and invalid UTF-8, which MongoDB accepted:
//   - `jsonb` refuses NUL outright, so a stored NUL would become a hard write
//     error the day any column changed type;
//   - invalid UTF-8 cannot be represented in a json column and would be
//     rejected by the server mid-transaction rather than at the edge.
func ValidateUploadFieldNames(value any) error {
	return validateNames(value, 0)
}

const maxValidateDepth = 256

func validateNames(value any, depth int) error {
	if depth > maxValidateDepth {
		return fmt.Errorf("gamedata: upload nesting deeper than %d", maxValidateDepth)
	}
	switch v := value.(type) {
	case map[string]any:
		for k, val := range v {
			if err := validateFieldName(k); err != nil {
				return err
			}
			if err := validateNames(val, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := validateNames(item, depth+1); err != nil {
				return err
			}
		}
	case string:
		if !utf8.ValidString(v) {
			return fmt.Errorf("gamedata: invalid UTF-8 in a string value")
		}
		if strings.ContainsRune(v, 0) {
			return fmt.Errorf("gamedata: NUL in a string value")
		}
	}
	return nil
}

func validateFieldName(k string) error {
	if strings.ContainsAny(k, ".$") {
		return fmt.Errorf("gamedata: invalid field name %q", k)
	}
	if strings.ContainsRune(k, 0) {
		return fmt.Errorf("gamedata: NUL in field name")
	}
	if !utf8.ValidString(k) {
		return fmt.Errorf("gamedata: invalid UTF-8 in field name")
	}
	return nil
}

// checkLimits enforces Limits over the encoded columns of one upload.
func checkLimits(l Limits, perKey map[string]int, extraKeys, extraBytes, total int) error {
	if l.MaxKeyBytes > 0 {
		for k, n := range perKey {
			if n > l.MaxKeyBytes {
				return fmt.Errorf("gamedata: key %q is %d bytes, over the %d byte limit", k, n, l.MaxKeyBytes)
			}
		}
	}
	if l.MaxExtraKeys > 0 && extraKeys > l.MaxExtraKeys {
		return fmt.Errorf("gamedata: upload carries %d unknown top-level keys, over the %d limit",
			extraKeys, l.MaxExtraKeys)
	}
	if l.MaxExtraBytes > 0 && extraBytes > l.MaxExtraBytes {
		return fmt.Errorf("gamedata: unknown keys total %d bytes, over the %d byte limit",
			extraBytes, l.MaxExtraBytes)
	}
	if l.MaxRowBytes > 0 && total > l.MaxRowBytes {
		return fmt.Errorf("gamedata: upload totals %d bytes, over the %d byte limit", total, l.MaxRowBytes)
	}
	return nil
}
