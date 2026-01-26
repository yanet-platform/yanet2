package main

import (
	"fmt"
	"os"

	"github.com/stretchr/testify/assert/yaml"
)

func main() {
	var cfgPath string
	if len(os.Args) == 2 {
		cfgPath = os.Args[1]
	} else {
		fmt.Fprintf(os.Stderr, "usage: %s config.yaml\n", os.Args[0])
		os.Exit(2)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read config %q: %v\n", cfgPath, err)
		os.Exit(1)
	}

	var cfg BenchConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "parse yaml: %v\n", err)
		os.Exit(1)
	}

	if err := Run(&cfg); err != nil {
		fmt.Fprintf(os.Stderr, "run failed: %v\n", err)
		os.Exit(1)
	} else {
		fmt.Println("run is successful")
	}
}
