//go:build ignore

package main

import (
	"log"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
)

func main() {
	if err := entc.Generate("./schema", &gen.Config{
		Package: "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql",
		Target:  "../../utils/database/postgresql",
	}); err != nil {
		log.Fatal("running ent codegen:", err)
	}
}
