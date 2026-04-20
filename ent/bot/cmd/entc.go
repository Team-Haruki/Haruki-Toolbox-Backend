//go:build ignore

package main

import (
	"log"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
)

func main() {
	if err := entc.Generate("./schema", &gen.Config{
		Package: "haruki-suite/utils/database/neopg",
		Target:  "../../utils/database/neopg",
	}); err != nil {
		log.Fatal("running ent codegen:", err)
	}
}
