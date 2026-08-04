package lab

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

//go:embed yanet2-lab.schema.json
var schemaFiles embed.FS

const schemaURL = "https://github.com/yanet-platform/yanet2/lab/yanet2-lab.schema.json"

type Manifest struct {
	Schema      string  `yaml:"$schema,omitempty" json:"$schema,omitempty"`
	Version     int     `yaml:"version" json:"version"`
	Name        string  `yaml:"name" json:"name"`
	Description string  `yaml:"description,omitempty" json:"description,omitempty"`
	Boot        Boot    `yaml:"boot,omitempty" json:"boot"`
	Files       []File  `yaml:"files,omitempty" json:"files,omitempty"`
	Steps       []Step  `yaml:"steps,omitempty" json:"steps,omitempty"`
	Probes      []Probe `yaml:"probes,omitempty" json:"probes,omitempty"`
}

type Boot struct {
	Dataplane    string `yaml:"dataplane,omitempty" json:"dataplane,omitempty"`
	Controlplane string `yaml:"controlplane,omitempty" json:"controlplane,omitempty"`
}

type File struct {
	Source      string `yaml:"source" json:"source"`
	Destination string `yaml:"destination" json:"destination"`
}

type Step struct {
	Name    string   `yaml:"name" json:"name"`
	Argv    []string `yaml:"argv" json:"argv"`
	Timeout string   `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

type Probe struct {
	Name    string      `yaml:"name" json:"name"`
	Ingress int         `yaml:"ingress" json:"ingress"`
	Egress  int         `yaml:"egress" json:"egress"`
	Timeout string      `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Send    ProbeSend   `yaml:"send" json:"send"`
	Expect  ProbeExpect `yaml:"expect" json:"expect"`
}

type ProbeSend struct {
	PCAP string `yaml:"pcap" json:"pcap"`
}

type ProbeExpect struct {
	PCAP string `yaml:"pcap,omitempty" json:"pcap,omitempty"`
	Drop bool   `yaml:"drop,omitempty" json:"drop,omitempty"`
}

func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return ParseManifest(data, filepath.Dir(path))
}

func ParseManifest(data []byte, baseDir string) (*Manifest, error) {
	jsonDocument, err := yamlToJSON(data)
	if err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	schema, err := compiledSchema()
	if err != nil {
		return nil, fmt.Errorf("compile manifest schema: %w", err)
	}
	var document any
	if err := json.Unmarshal(jsonDocument, &document); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := schema.Validate(document); err != nil {
		return nil, fmt.Errorf("manifest does not match schema: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(jsonDocument, &manifest); err != nil {
		return nil, fmt.Errorf("decode validated manifest: %w", err)
	}
	if err := validateManifestSemantics(&manifest, baseDir); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func compiledSchema() (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	contents, err := schemaFiles.ReadFile("yanet2-lab.schema.json")
	if err != nil {
		return nil, err
	}
	var document any
	if err := json.Unmarshal(contents, &document); err != nil {
		return nil, err
	}
	if err := compiler.AddResource(schemaURL, document); err != nil {
		return nil, err
	}
	return compiler.Compile(schemaURL)
}

func yamlToJSON(data []byte) ([]byte, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple YAML documents are not supported")
		}
		return nil, err
	}
	return json.Marshal(document)
}

func validateManifestSemantics(manifest *Manifest, baseDir string) error {
	names := map[string]string{}
	for _, step := range manifest.Steps {
		if previous, ok := names[step.Name]; ok {
			return fmt.Errorf("duplicate name %q in %s and step", step.Name, previous)
		}
		names[step.Name] = "step"
		if err := validateDuration(step.Timeout); err != nil {
			return fmt.Errorf("step %q timeout: %w", step.Name, err)
		}
	}
	for _, probe := range manifest.Probes {
		if previous, ok := names[probe.Name]; ok {
			return fmt.Errorf("duplicate name %q in %s and probe", probe.Name, previous)
		}
		names[probe.Name] = "probe"
		if err := validateDuration(probe.Timeout); err != nil {
			return fmt.Errorf("probe %q timeout: %w", probe.Name, err)
		}
	}

	paths := []string{manifest.Boot.Dataplane, manifest.Boot.Controlplane}
	for _, file := range manifest.Files {
		paths = append(paths, file.Source)
	}
	for _, probe := range manifest.Probes {
		paths = append(paths, probe.Send.PCAP, probe.Expect.PCAP)
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := validateLocalPath(baseDir, path); err != nil {
			return err
		}
	}
	return nil
}

func validateDuration(value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.ParseDuration(value); err != nil {
		return err
	}
	return nil
}

func validateLocalPath(baseDir, value string) error {
	if filepath.IsAbs(value) {
		return fmt.Errorf("path %q must be relative to the manifest", value)
	}
	clean := filepath.Clean(value)
	if clean == ".." || filepath.IsAbs(clean) || len(clean) >= 3 && clean[:3] == ".."+string(filepath.Separator) {
		return fmt.Errorf("path %q escapes the manifest directory", value)
	}
	path := filepath.Join(baseDir, clean)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("path %q does not exist", value)
		}
		return fmt.Errorf("inspect path %q: %w", value, err)
	}
	realBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return fmt.Errorf("resolve manifest directory: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", value, err)
	}
	relative, err := filepath.Rel(realBase, realPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes the manifest directory through a symbolic link", value)
	}
	return nil
}
