package main

import (
	"flag"
	"fmt"
	"os"

	"haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	"haruki-suite/utils/nuversestruct"

	"gopkg.in/yaml.v3"
)

func main() {
	var samplePath string
	var baselineSchemaPath string
	var configPath string
	var schemaPath string
	var inputFormat string
	var serverRaw string
	var generateOnly bool

	flag.StringVar(&samplePath, "sample", "", "path to local suite sample")
	flag.StringVar(&baselineSchemaPath, "baseline-schema", "", "optional baseline StructTool/Avro schema for restore diff")
	flag.StringVar(&configPath, "config", "", "path to haruki config; defaults to env/default config path")
	flag.StringVar(&schemaPath, "schema", "data/suite_user.avsc", "path to StructTool/Avro schema json")
	flag.StringVar(&inputFormat, "input-format", nuversestruct.InputFormatMsgpack, "sample format: msgpack or raw-upload")
	flag.StringVar(&serverRaw, "server", "", "server for raw-upload samples: jp, en, tw, kr, or cn")
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

	server, err := parseServerForInput(inputFormat, serverRaw)
	if err != nil {
		fatalf("%v", err)
	}
	if inputFormat == nuversestruct.InputFormatRawUpload {
		loadedPath, err := loadConfig(configPath)
		if err != nil {
			fatalf("%v", err)
		}
		fmt.Fprintf(os.Stderr, "nuverse-restore-compare: loaded config %s\n", loadedPath)
	}

	report, err := nuversestruct.CompareSuiteRestore(nuversestruct.CompareOptions{
		SampleMsgpackPath:  samplePath,
		BaselineSchemaPath: baselineSchemaPath,
		SchemaPath:         schemaPath,
		InputFormat:        inputFormat,
		Server:             server,
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

func parseServerForInput(inputFormat string, serverRaw string) (harukiUtils.SupportedDataUploadServer, error) {
	if inputFormat != nuversestruct.InputFormatRawUpload {
		return "", nil
	}
	if serverRaw == "" {
		return "", fmt.Errorf("-server is required when -input-format=raw-upload")
	}
	server, err := harukiUtils.ParseSupportedDataUploadServer(serverRaw)
	if err != nil {
		return "", err
	}
	return server, nil
}

func loadConfig(configPath string) (string, error) {
	if configPath != "" {
		if err := config.LoadGlobal(configPath); err != nil {
			if fallbackErr := loadSekaiClientOnly(configPath); fallbackErr != nil {
				return configPath, fmt.Errorf("load config %q: %w; sekai_client fallback failed: %v", configPath, err, fallbackErr)
			}
			return configPath + " (sekai_client only)", nil
		}
		return configPath, nil
	}
	loadedPath, err := config.LoadGlobalFromEnvOrDefault()
	if err != nil {
		if fallbackErr := loadSekaiClientOnly(loadedPath); fallbackErr != nil {
			return loadedPath, fmt.Errorf("load config %q: %w; sekai_client fallback failed: %v", loadedPath, err, fallbackErr)
		}
		return loadedPath + " (sekai_client only)", nil
	}
	return loadedPath, nil
}

func loadSekaiClientOnly(configPath string) error {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var partial struct {
		SekaiClient config.SekaiClientConfig `yaml:"sekai_client"`
	}
	if err := yaml.Unmarshal(content, &partial); err != nil {
		return err
	}
	config.Cfg.SekaiClient = partial.SekaiClient
	return nil
}
