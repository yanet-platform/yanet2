//go:build linux && (amd64 || arm64)

// Command yanet-native inspects a running YANET dataplane through its
// shared memory, without cgo: it maps the storage file and prints the
// instances, workers, module types, published module configurations,
// agents, counters and dataplane time it finds, as JSON.
//
// It is a developer tool that reads as the control plane does, so it
// writes to the storage too: it needs write access to the file, takes the
// C locks (the dataplane zone's configuration lock to list module types,
// the publication lock to pin a generation, the agent lock to list
// agents) and raises the pinned generation's reference count while it
// reads. Each instance is read on its own, so the report is no snapshot
// of the whole storage. A generation a publication retires while pinned
// keeps the reference this tool took and is not freed until native
// generation teardown lands.
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
	// Counters holds every counter storage the generation publishes.
	Counters []zone.CounterGroup `json:"counters"`
	// CurrentTime is the latest dataplane time in nanoseconds, absent
	// before any worker published one.
	CurrentTime *uint64 `json:"current_time,omitempty"`
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

	report := Report{Instances: []InstanceReport{}}
	for _, instance := range storage.Instances {
		workers, err := instance.Workers()
		if err != nil {
			return err
		}
		entry := InstanceReport{
			Index:            instance.Index,
			NumaNode:         instance.Dp.NumaIdx,
			Workers:          orEmpty(workers),
			DataplaneModules: orEmpty(instance.DataplaneModules()),
			Agents:           orEmpty(instance.Agents()),
		}
		if now, ok := instance.CurrentTime(); ok {
			entry.CurrentTime = &now
		}
		if err := readGeneration(instance, &entry); err != nil {
			return err
		}
		report.Instances = append(report.Instances, entry)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

// readGeneration fills in what the instance's published generation holds,
// read under one pin.
func readGeneration(instance *zone.Instance, entry *InstanceReport) error {
	generation, err := instance.Pin()
	if err != nil {
		return err
	}
	defer generation.Release()
	if generation.Gen != nil {
		entry.Generation = generation.Gen.Gen
	}
	entry.Modules = orEmpty(generation.Modules())
	counters, err := generation.Counters(nil)
	if err != nil {
		return err
	}
	entry.Counters = orEmpty(counters)
	return nil
}

// orEmpty returns an empty list for nil, so that it prints as [].
func orEmpty[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}
