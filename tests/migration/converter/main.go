package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/yanet-platform/yanet2/tests/migration/converter/lib"
)

func main() {
	var (
		inputDir   = flag.String("input", "", "Path to yanet1 tests directory (e.g., yanet1/autotest/units)")
		outputDir  = flag.String("output", "", "Path to directory for generated yanet2 tests")
		testName   = flag.String("test", "", "Name of specific test to convert (optional)")
		batch      = flag.Bool("batch", false, "Convert all tests in directory")
		verbose    = flag.Bool("v", false, "Verbose output")
		debug      = flag.Bool("debug", false, "Enable debug logging for conversions (automatically enables verbose)")
		statsFile  = flag.String("stats", "", "File to save statistics (markdown)")
		skiplist   = flag.String("skiplist", "", "Path to skiplist YAML (optional)")
		updateSkip = flag.Bool("update-skiplist", false, "Update skiplist.yaml in-place at the auto-generated marker")
		forceAST   = flag.Bool("force-ast", false, "Force use of AST parser (fail if unavailable)")
		strict     = flag.Bool("strict", false, "Strict mode: fail on unsupported layers/special handling (for CI)")
		tolerant   = flag.Bool("tolerant", true, "Tolerant mode: continue with warnings on unsupported features (default)")
	)
	flag.Parse()

	// Debug mode automatically enables verbose output
	if *debug {
		*verbose = true
	}

	if *inputDir == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -input <yanet1_tests_dir> [-output <yanet2_tests_dir>] [-test <test_name>] [-batch] [-v] [-stats <file>] [-skiplist <file>]\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Default output dir if not provided
	if *outputDir == "" {
		*outputDir = filepath.Join("..", "..", "functional", "converted")
	}

	// Default skiplist path if not provided and file exists one level up
	if *skiplist == "" {
		def := filepath.Join("..", "skiplist.yaml")
		if _, err := os.Stat(def); err == nil {
			*skiplist = def
		}
	}

	converter, err := lib.NewConverter(&lib.Config{
		InputDir:       *inputDir,
		OutputDir:      *outputDir,
		Verbose:        *verbose,
		Debug:          *debug,
		SkiplistPath:   *skiplist,
		ForceASTParser: *forceAST,
		StrictMode:     *strict,
		TolerantMode:   *tolerant,
	})
	if err != nil {
		log.Fatalf("Failed to create converter: %v", err)
	}

	if *updateSkip {
		if err := converter.UpdateSkiplist(); err != nil {
			log.Fatalf("Error updating skiplist: %v", err)
		}
		fmt.Println("Skiplist updated successfully")
	} else if *testName != "" {
		// Convert single test
		// inputDir should already point to the test directory or its parent
		// Check if inputDir already contains the test, otherwise join
		testPath := *inputDir
		if filepath.Base(*inputDir) != *testName {
			testPath = filepath.Join(*inputDir, *testName)
		}
		if err := converter.ConvertSingleTest(testPath, *testName); err != nil {
			log.Fatalf("Error converting test %s: %v", *testName, err)
		}
		fmt.Printf("Test %s successfully converted\n", *testName)
	} else if *batch {
		// Batch convert all tests with statistics
		stats, err := converter.ConvertAllTestsWithStats()
		if err != nil {
			log.Fatalf("Error converting tests: %v", err)
		}

		// Print statistics
		stats.Print()

		// Save statistics to file if specified
		if *statsFile != "" {
			if err := stats.SaveToFile(*statsFile); err != nil {
				log.Fatalf("Error saving statistics: %v", err)
			}
		}
	} else {
		// Convert all tests (old method)
		if err := converter.ConvertAllTests(); err != nil {
			log.Fatalf("Error converting tests: %v", err)
		}
		fmt.Println("All tests successfully converted")
	}
}
