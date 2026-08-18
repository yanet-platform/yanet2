package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sync/errgroup"
)

func resolveTUPath(entry compileEntry) string {
	if filepath.IsAbs(entry.File) {
		return filepath.Clean(entry.File)
	}

	return filepath.Clean(filepath.Join(entry.Directory, entry.File))
}

func runCompiler(compiler string, args []string, dir string) (stdout string, err error) {
	cmd := exec.Command(compiler, args...)
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"failed to run %s in %s: %w\n%s", compiler, dir, err, errOut.String(),
		)
	}

	return out.String(), nil
}

func compileTU(
	entry compileEntry, ccOverride, objPath string, keepUnusedDebugTypes bool,
) (compiler string, linkFlags []string, err error) {
	tokens, err := shellSplit(entry.Command)
	if err != nil {
		return "", nil, err
	}

	compiler, args, err := transformCompileArgs(tokens, ccOverride, keepUnusedDebugTypes)
	if err != nil {
		return "", nil, err
	}

	linkFlags = linkDriverFlags(args)
	src := resolveTUPath(entry)
	args = append(args, "-c", src, "-o", objPath)
	if _, err := runCompiler(compiler, args, entry.Directory); err != nil {
		return "", nil, fmt.Errorf("failed to compile %s: %w", src, err)
	}

	return compiler, linkFlags, nil
}

func compileTUs(
	entries []compileEntry, ccOverride, workDir string, keepUnusedDebugTypes bool,
) (objPaths []string, linkCompiler string, linkFlags []string, err error) {
	objPaths = make([]string, len(entries))
	compilers := make([]string, len(entries))
	flagsByEntry := make([][]string, len(entries))
	errs := make([]error, len(entries))

	g := new(errgroup.Group)
	g.SetLimit(runtime.NumCPU())
	for idx, entry := range entries {
		g.Go(func() error {
			objPath := filepath.Join(workDir, fmt.Sprintf("tu%d.o", idx))
			compiler, flags, err := compileTU(entry, ccOverride, objPath, keepUnusedDebugTypes)
			objPaths[idx] = objPath
			compilers[idx] = compiler
			flagsByEntry[idx] = flags
			errs[idx] = err
			return nil
		})
	}

	g.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, "", nil, err
		}
	}

	if len(entries) > 0 {
		linkCompiler = compilers[0]
		linkFlags = flagsByEntry[0]
	}

	return objPaths, linkCompiler, linkFlags, nil
}

func linkSharedObject(compiler string, linkFlags, objPaths []string, workDir string) (soPath string, err error) {
	if compiler == "" {
		return "", fmt.Errorf("failed to link shared object: no compiler resolved from the compile database")
	}

	if len(objPaths) == 0 {
		return "", fmt.Errorf("failed to link shared object: no object files given")
	}

	soPath = filepath.Join(workDir, "abi.so")
	args := append([]string{"-shared", "-o", soPath}, linkFlags...)
	args = append(args, objPaths...)
	if _, err := runCompiler(compiler, args, workDir); err != nil {
		return "", fmt.Errorf("failed to link shared object: %w", err)
	}

	return soPath, nil
}

func headerDeps(entry compileEntry, ccOverride string) ([]string, error) {
	tokens, err := shellSplit(entry.Command)
	if err != nil {
		return nil, err
	}

	compiler, args, err := transformCompileArgs(tokens, ccOverride, true)
	if err != nil {
		return nil, err
	}

	src := resolveTUPath(entry)
	args = append(args, "-M", src)
	out, err := runCompiler(compiler, args, entry.Directory)
	if err != nil {
		return nil, fmt.Errorf("failed to list header dependencies for %s: %w", src, err)
	}

	return parseMakeDeps(out), nil
}

func splitMakeDepFields(unwrapped string) []string {
	var fields []string
	var cur strings.Builder
	runes := []rune(unwrapped)
	for idx := 0; idx < len(runes); idx++ {
		r := runes[idx]
		if r == '\\' && idx+1 < len(runes) && runes[idx+1] == ' ' {
			cur.WriteByte(' ')
			idx++
			continue
		}

		if r == ' ' || r == '\t' || r == '\n' {
			if cur.Len() > 0 {
				fields = append(fields, cur.String())
				cur.Reset()
			}

			continue
		}

		cur.WriteRune(r)
	}

	if cur.Len() > 0 {
		fields = append(fields, cur.String())
	}

	return fields
}

func parseMakeDeps(out string) []string {
	unwrapped := strings.ReplaceAll(out, "\\\n", " ")
	var deps []string
	for _, field := range splitMakeDepFields(unwrapped) {
		if strings.HasSuffix(field, ":") {
			continue
		}

		deps = append(deps, field)
	}

	return deps
}

func mkWorkDir() (string, func(), error) {
	dir, err := os.MkdirTemp("", "abigen-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create work directory: %w", err)
	}

	return dir, func() { os.RemoveAll(dir) }, nil
}
