// Command yanet-native inspects a running YANET dataplane through its
// shared memory, without cgo: it maps the storage file and prints the
// instances, workers, module types, published module configurations and
// agents it finds, as JSON.
//
// Usage:
//
//	yanet-native -storage /dev/hugepages/yanet
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// Report is what yanet-native prints.
type Report struct {
	Instances []InstanceReport `json:"instances"`
}

// InstanceReport describes one dataplane instance.
type InstanceReport struct {
	Index            int                    `json:"index"`
	NumaNode         uint32                 `json:"numa_node"`
	Workers          []zone.Worker          `json:"workers"`
	DataplaneModules []zone.DataplaneModule `json:"dataplane_modules"`
	Generation       uint64                 `json:"generation"`
	Modules          []zone.Module          `json:"modules"`
	Agents           []zone.Agent           `json:"agents"`
}

func main() {
	path := flag.String("storage", "/dev/hugepages/yanet", "dataplane storage file")
	flag.Parse()
	if err := run(*path); err != nil {
		fmt.Fprintf(os.Stderr, "yanet-native: %v\n", err)
		os.Exit(1)
	}
}

func run(path string) error {
	storage, err := zone.Attach(path)
	if err != nil {
		return err
	}
	defer storage.Close()

	var report Report
	for _, instance := range storage.Instances {
		workers, err := instance.Workers()
		if err != nil {
			return err
		}
		entry := InstanceReport{
			Index:            instance.Index,
			NumaNode:         instance.Dp.NumaIdx,
			Workers:          workers,
			DataplaneModules: instance.DataplaneModules(),
			Agents:           instance.Agents(),
		}
		generation := instance.Pin()
		if generation.Gen != nil {
			entry.Generation = generation.Gen.Gen
		}
		entry.Modules = generation.Modules()
		generation.Release()
		report.Instances = append(report.Instances, entry)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
