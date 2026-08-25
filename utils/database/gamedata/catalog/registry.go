package catalog

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

// The pinned registries are compiled into the binary. Column identifiers must be
// a compile-time constant of the build: deriving them at runtime from stored
// documents or request input is exactly the hole this package exists to close.
//
// Regenerating them is a deliberate act — see cmd/gamedata-migrate `survey`.
// A key added to the game after this build simply lands in `extra` and is
// reported; it never invents a column.

//go:embed registry-suite.json
var suiteRegistryJSON []byte

//go:embed registry-mysekai.json
var mysekaiRegistryJSON []byte

var (
	suite   *Catalog
	mysekai *Catalog
)

func init() {
	var err error
	if suite, err = parse(suiteRegistryJSON); err != nil {
		panic("gamedata/catalog: suite registry: " + err.Error())
	}
	if mysekai, err = parse(mysekaiRegistryJSON); err != nil {
		panic("gamedata/catalog: mysekai registry: " + err.Error())
	}
}

func parse(b []byte) (*Catalog, error) {
	var c Catalog
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if err := c.build(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Suite is the pinned catalog for the suite table.
func Suite() *Catalog { return suite }

// Mysekai is the pinned catalog for the mysekai table.
func Mysekai() *Catalog { return mysekai }

// For returns the catalog for a collection name ("suite" / "mysekai").
func For(collection string) (*Catalog, bool) {
	switch collection {
	case suite.Collection:
		return suite, true
	case mysekai.Collection:
		return mysekai, true
	}
	return nil, false
}
