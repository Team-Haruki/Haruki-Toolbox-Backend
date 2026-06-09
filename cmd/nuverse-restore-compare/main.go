package main

import (
	"flag"
	"fmt"
	"os"

	"haruki-suite/utils/nuversestruct"
)

func main() {
	var samplePath string
	var currentStructuresPath string
	var schemaPath string
	var generateOnly bool

	flag.StringVar(&samplePath, "sample", "", "path to local suite msgpack sample")
	flag.StringVar(&currentStructuresPath, "current-structures", "data/suite_structures.json", "path to current suite structure json")
	flag.StringVar(&schemaPath, "schema", "", "path to StructTool/Avro schema json")
	flag.BoolVar(&generateOnly, "generate-only", false, "print generated suite structures instead of comparing a sample")
	flag.Parse()

	if schemaPath == "" {
		fatalf("-schema is required")
	}

	if generateOnly {
		schemaBytes, err := os.ReadFile(schemaPath)
		if err != nil {
			fatalf("read schema: %v", err)
		}
		out, err := nuversestruct.MarshalGeneratedStructuresFromSchema(schemaBytes)
		if err != nil {
			fatalf("generate structures: %v", err)
		}
		_, _ = os.Stdout.Write(out)
		return
	}

	report, err := nuversestruct.CompareSuiteRestore(nuversestruct.CompareOptions{
		SampleMsgpackPath:     samplePath,
		CurrentStructuresPath: currentStructuresPath,
		SchemaPath:            schemaPath,
	})
	if err != nil {
		fatalf("%v", err)
	}
	out, err := report.MarshalJSONDeterministic()
	if err != nil {
		fatalf("marshal report: %v", err)
	}
	fmt.Println(string(out))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "nuverse-restore-compare: "+format+"\n", args...)
	os.Exit(1)
}
