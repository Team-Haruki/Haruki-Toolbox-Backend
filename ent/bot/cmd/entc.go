//go:build ignore

package main

import (
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/ent/codegen"
	"log"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
)

func main() {
	if err := entc.Generate("./schema", &gen.Config{
		Hooks:   []gen.Hook{codegen.Modernize},
		Package: "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/neopg",
		Target:  "../../utils/database/neopg",
	}); err != nil {
		log.Fatal("running ent codegen:", err)
	}
}
