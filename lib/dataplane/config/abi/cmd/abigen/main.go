package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type stringListFlag struct {
	// Values accumulates one entry per repeated flag occurrence.
	//
	// Entries are kept in the order flag.Parse saw them.
	Values []string
}

func (m *stringListFlag) String() string {
	return strings.Join(m.Values, ",")
}

func (m *stringListFlag) Set(value string) error {
	m.Values = append(m.Values, value)
	return nil
}

type config struct {
	// CompileCommands is the path to compile_commands.json.
	//
	// abigen reads TU compile flags from it.
	CompileCommands string
	// RepoRoot is the directory manifest and depfile paths are relative to.
	RepoRoot string
	// CC overrides the compiler compile_commands.json recorded, when set.
	CC string
	// ManifestOut is a path the manifest text is also written to.
	//
	// It is human-readable, and written besides stdout.
	ManifestOut string
	// EmitC is the path the generated abi_manifest.c source is written to.
	EmitC string
	// Depfile is the path a make-style depfile is written to.
	//
	// It lists every input the manifest depends on.
	Depfile string
	// SymbolPrefix names the symbols generateManifestC emits.
	//
	// It covers both the entity-table and manifest symbols.
	SymbolPrefix string
	// TUPatterns selects translation units by glob when TUPaths is empty.
	TUPatterns []string
	// TUPaths, when given, are the exact translation units to hash.
	//
	// They replace pattern-based discovery.
	TUPaths []string
	// Plugin selects plugin mode.
	//
	// It hashes only the entities the given translation units themselves
	// reference.
	Plugin bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	result, err := generate(cfg, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	if cfg.ManifestOut == "" && cfg.EmitC == "" {
		fmt.Fprint(stdout, result.ManifestText)
	}

	if cfg.ManifestOut != "" {
		if err := os.WriteFile(cfg.ManifestOut, []byte(result.ManifestText), 0o644); err != nil {
			fmt.Fprintln(stderr, fmt.Errorf("failed to write manifest: %w", err))
			return 1
		}
	}

	if cfg.EmitC != "" {
		if err := os.WriteFile(cfg.EmitC, []byte(result.ManifestC), 0o644); err != nil {
			fmt.Fprintln(stderr, fmt.Errorf("failed to write manifest C source: %w", err))
			return 1
		}
	}

	if cfg.Depfile != "" {
		if err := writeDepfile(cfg.Depfile, depfileTarget(cfg), result.DepfileInputs); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}

	return 0
}

func parseArgs(args []string) (config, error) {
	fs := flag.NewFlagSet("abigen", flag.ContinueOnError)
	cwd, err := os.Getwd()
	if err != nil {
		return config{}, fmt.Errorf("failed to resolve working directory: %w", err)
	}

	var cfg config
	var patterns stringListFlag
	fs.StringVar(&cfg.CompileCommands, "compile-commands", filepath.Join(cwd, "build", "compile_commands.json"), "path to compile_commands.json")
	fs.StringVar(&cfg.RepoRoot, "repo-root", cwd, "repository root the manifest paths are relative to")
	fs.StringVar(&cfg.CC, "cc", "", "override the compiler binary recorded in compile_commands.json")
	fs.StringVar(&cfg.ManifestOut, "o", "", "write the manifest text to this path in addition to stdout")
	fs.StringVar(&cfg.EmitC, "emit-c", "", "write abi_manifest.c to this path")
	fs.StringVar(&cfg.Depfile, "depfile", "", "write a make-style depfile listing every TU and header the manifest depends on")
	fs.StringVar(&cfg.SymbolPrefix, "symbol-prefix", defaultSymbolPrefix, "prefix for the hash/manifest symbol names emitted by -emit-c")
	fs.Var(&patterns, "tu-pattern", "glob (relative to -repo-root, matched against compile_commands.json entries) selecting TUs; repeatable, ignored if TU paths are given positionally")
	fs.BoolVar(&cfg.Plugin, "plugin", false, "plugin mode: hash only the entities the given translation units reference, from their own compile")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	cfg.TUPaths = fs.Args()
	cfg.TUPatterns = patterns.Values
	if len(cfg.TUPatterns) == 0 {
		cfg.TUPatterns = defaultTUPatterns
	}

	if cfg.Plugin && len(cfg.TUPaths) == 0 {
		return config{}, fmt.Errorf("plugin mode requires one or more explicit translation unit paths")
	}

	return cfg, nil
}

type generateResult struct {
	// ManifestText is the manifest dump run writes to stdout or -o.
	//
	// It is human-readable and hash-prefixed.
	ManifestText string
	// ManifestC is the generated C source run writes to -emit-c.
	ManifestC string
	// DepfileInputs lists every path the manifest depends on.
	//
	// It feeds the depfile run writes to -depfile.
	DepfileInputs []string
}

func generate(cfg config, stderr io.Writer) (generateResult, error) {
	db, err := loadCompileDB(cfg.CompileCommands)
	if err != nil {
		return generateResult{}, err
	}

	entries, err := resolveTUEntries(cfg, db)
	if err != nil {
		return generateResult{}, err
	}

	workDir, cleanup, err := mkWorkDir()
	if err != nil {
		return generateResult{}, err
	}

	defer cleanup()

	scan, err := scanHeaders(entries, cfg.CC)
	if err != nil {
		return generateResult{}, err
	}

	depInputs := append([]string{}, scan.DepfileInputs...)
	depInputs = append(depInputs, cfg.CompileCommands)

	packageInputs, err := abigenPackageInputs(cfg.RepoRoot)
	if err != nil {
		return generateResult{}, err
	}

	depInputs = append(depInputs, packageInputs...)

	var rows []entityRow
	if cfg.Plugin {
		rows, err = generatePluginRows(cfg, entries, workDir)
	} else {
		var contractHeaders []string
		rows, contractHeaders, err = generateBinaryRows(cfg, entries, workDir)
		for _, rel := range contractHeaders {
			depInputs = append(depInputs, filepath.Join(cfg.RepoRoot, rel))
		}
	}

	if err != nil {
		return generateResult{}, err
	}

	manifestText := buildManifestText(rows)

	sort.Strings(depInputs)

	fmt.Fprintf(stderr, "abigen: %d entities\n", len(rows))

	return generateResult{
		ManifestText:  manifestText,
		ManifestC:     generateManifestC(rows, manifestText, cfg.SymbolPrefix),
		DepfileInputs: depInputs,
	}, nil
}

func generateBinaryRows(cfg config, entries []compileEntry, workDir string) ([]entityRow, []string, error) {
	if len(entries) == 0 {
		return nil, nil, fmt.Errorf("no translation units to source the contract header probe's compile flags from")
	}

	objPaths, linkCompiler, linkFlags, err := compileTUs(entries, cfg.CC, workDir, true)
	if err != nil {
		return nil, nil, err
	}

	soPath, err := linkSharedObject(linkCompiler, linkFlags, objPaths, workDir)
	if err != nil {
		return nil, nil, err
	}

	functions, err := extractFunctions(soPath, cfg.RepoRoot)
	if err != nil {
		return nil, nil, err
	}

	contractHeaders, err := discoverContractHeaders(cfg.RepoRoot)
	if err != nil {
		return nil, nil, err
	}

	templateEntry, err := selectContractTemplate(entries)
	if err != nil {
		return nil, nil, err
	}

	headerSoPath, err := buildContractHeaderProbe(templateEntry, cfg.CC, workDir, contractHeaders)
	if err != nil {
		return nil, nil, err
	}

	types, excludedTypeNames, unresolvedDeclPaths, err := extractTypes(headerSoPath, cfg.RepoRoot)
	if err != nil {
		return nil, nil, err
	}

	if len(types) == 0 {
		return nil, nil, zeroTypesError(cfg.RepoRoot, unresolvedDeclPaths)
	}

	if err := validateTypeCoverage(types, functions, excludedTypeNames); err != nil {
		return nil, nil, err
	}

	return buildEntityRows(types, functions), contractHeaders, nil
}

func generatePluginRows(cfg config, entries []compileEntry, workDir string) ([]entityRow, error) {
	soPath, err := buildPluginProbe(entries, cfg.CC, workDir)
	if err != nil {
		return nil, err
	}

	types, _, _, err := extractTypes(soPath, cfg.RepoRoot)
	if err != nil {
		return nil, err
	}

	functions, err := extractPluginFunctions(soPath, cfg.RepoRoot)
	if err != nil {
		return nil, err
	}

	rows := buildEntityRows(types, functions)
	if len(rows) == 0 {
		return nil, fmt.Errorf(
			"extracted zero ABI entities from plugin translation units: %s",
			strings.Join(examinedTUs(entries), ", "),
		)
	}

	return rows, nil
}

func examinedTUs(entries []compileEntry) []string {
	tus := make([]string, len(entries))
	for idx, entry := range entries {
		tus[idx] = resolveTUPath(entry)
	}

	return tus
}

func zeroTypesError(repoRoot string, unresolvedDeclPaths []string) error {
	if len(unresolvedDeclPaths) == 0 {
		return fmt.Errorf("extracted zero ABI-boundary types against -repo-root %s", repoRoot)
	}

	return fmt.Errorf(
		"extracted zero ABI-boundary types against -repo-root %s; declaration paths did not resolve under it, e.g. %s",
		repoRoot, strings.Join(unresolvedDeclPaths, ", "),
	)
}

func resolveTUEntries(cfg config, db compileDB) ([]compileEntry, error) {
	if len(cfg.TUPaths) > 0 {
		entries := make([]compileEntry, 0, len(cfg.TUPaths))
		for _, tu := range cfg.TUPaths {
			abs := tu
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(cfg.RepoRoot, tu)
			}

			entry, err := db.Lookup(filepath.Clean(abs))
			if err != nil {
				return nil, err
			}

			entries = append(entries, entry)
		}

		return entries, nil
	}

	return discoverTUs(db, cfg.RepoRoot, cfg.TUPatterns)
}
