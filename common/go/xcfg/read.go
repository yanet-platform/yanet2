package xcfg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	maxIncludeDepth = 32
	maxIncludeFiles = 256
	maxIncludeBytes = 16 << 20
)

// ReadFile composes a YAML configuration and its top-level include list.
//
// Entries name files or directories, relative to the declaring file. Directories
// contribute visible .yaml/.yml files recursively in lexical order. Later
// includes override earlier ones; the declaring file overrides its includes.
// Mappings merge recursively; other values, including lists, replace. Files
// without includes retain their original bytes and diagnostic line numbers.
func ReadFile(path string) ([]byte, error) {
	reader := &configReader{Active: map[string]bool{}}
	config, data, included, err := reader.Read(path, 0)
	if err != nil {
		return nil, err
	}
	if !included {
		return data, nil
	}
	return yaml.Marshal(config)
}

type configReader struct {
	Active map[string]bool
	Files  int
	Bytes  int64
}

// Read bounds composition and detects cycles through real file identities.
func (m *configReader) Read(path string, depth int) (any, []byte, bool, error) {
	if depth > maxIncludeDepth {
		return nil, nil, false, fmt.Errorf("%s: include depth exceeds %d", path, maxIncludeDepth)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, nil, false, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, nil, false, err
	}
	if m.Active[canonical] {
		return nil, nil, false, fmt.Errorf("%s: include cycle", path)
	}
	m.Active[canonical] = true
	defer delete(m.Active, canonical)

	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, false, err
	}
	if info.IsDir() {
		config, err := m.ReadDirectory(path, depth)
		return config, nil, true, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, false, fmt.Errorf("%s: include must be a regular file or directory", path)
	}
	m.Files++
	if m.Files > maxIncludeFiles {
		return nil, nil, false, fmt.Errorf("%s: includes exceed %d files", path, maxIncludeFiles)
	}
	data, err := m.ReadBytes(path)
	if err != nil {
		return nil, nil, false, err
	}
	value, err := parseConfigValue(data)
	if err != nil {
		return nil, nil, false, fmt.Errorf("%s: %w", path, err)
	}
	config, mapping := configMapping(value)
	if !mapping {
		if value != nil && depth > 0 {
			return nil, nil, false, fmt.Errorf("%s: included configuration must be a mapping", path)
		}
		return value, data, false, nil
	}
	include, included := config["include"]
	if !included {
		return config, data, false, nil
	}
	delete(config, "include")
	entries, ok := include.([]any)
	if !ok {
		return nil, nil, false, fmt.Errorf("%s: include must be a list of paths", path)
	}
	var merged any = map[any]any{}
	for _, entry := range entries {
		name, ok := entry.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return nil, nil, false, fmt.Errorf("%s: include entries must be non-empty strings", path)
		}
		if !filepath.IsAbs(name) {
			name = filepath.Join(filepath.Dir(path), name)
		}
		fragment, _, _, err := m.Read(name, depth+1)
		if err != nil {
			return nil, nil, false, fmt.Errorf("%s: include %q: %w", path, entry, err)
		}
		if fragment != nil {
			merged = mergeConfig(merged, fragment)
		}
	}
	return mergeConfig(merged, config), data, true, nil
}

// ReadBytes caps the total input, including files reached more than once.
func (m *configReader) ReadBytes(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxIncludeBytes-m.Bytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.Bytes += int64(len(data))
	if m.Bytes > maxIncludeBytes {
		return nil, fmt.Errorf("%s: includes exceed %d bytes", path, maxIncludeBytes)
	}
	return data, nil
}

// ReadDirectory follows projected keys while ignoring hidden generations.
func (m *configReader) ReadDirectory(path string, depth int) (any, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var merged any = map[any]any{}
	filesBefore := m.Files
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		name := filepath.Join(path, entry.Name())
		info, err := os.Stat(name)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() && filepath.Ext(name) != ".yaml" && filepath.Ext(name) != ".yml" {
			continue
		}
		fragment, _, _, err := m.Read(name, depth+1)
		if err != nil {
			return nil, err
		}
		if fragment != nil {
			merged = mergeConfig(merged, fragment)
		}
	}
	if m.Files == filesBefore {
		return nil, fmt.Errorf("%s: include directory contains no YAML files", path)
	}
	return merged, nil
}

func parseConfigValue(data []byte) (any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var config any
	for {
		var document any
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			return config, nil
		}
		if err != nil {
			return nil, err
		}
		if document != nil {
			if config != nil {
				return nil, fmt.Errorf("configuration must contain a single YAML document")
			}
			config = document
		}
	}
}

func configMapping(value any) (map[any]any, bool) {
	switch value := value.(type) {
	case map[string]any:
		mapping := map[any]any{}
		for key, entry := range value {
			mapping[key] = entry
		}
		return mapping, true
	case map[any]any:
		return value, true
	default:
		return nil, false
	}
}

func mergeConfig(base, overlay any) any {
	baseMapping, baseOK := configMapping(base)
	overlayMapping, overlayOK := configMapping(overlay)
	if baseOK && overlayOK {
		for key, value := range overlayMapping {
			baseMapping[key] = mergeConfig(baseMapping[key], value)
		}
		return baseMapping
	}
	return overlay
}
