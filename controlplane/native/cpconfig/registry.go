//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// The keys of a generation's registries: what each kind of item is
// hashed and found by.

func moduleHash(item *layout.RegistryItem) uint64 {
	module := registry.Container[layout.CpModule](item, 0)
	return registry.HashNamePair(shm.Bytes(module.Name[:]), shm.Bytes(module.Type[:]))
}

func deviceHash(item *layout.RegistryItem) uint64 {
	device := registry.Container[layout.CpDevice](item, 0)
	return registry.HashNamePair(shm.Bytes(device.Name[:]), shm.Bytes(device.Type[:]))
}

func objectHash(item *layout.RegistryItem) uint64 {
	object := registry.Container[layout.CpObject](item, 0)
	return registry.HashNamePair(shm.Bytes(object.Name[:]), shm.Bytes(object.Type[:]))
}

func functionHash(item *layout.RegistryItem) uint64 {
	return registry.HashName(shm.Bytes(registry.Function(item).Name[:]))
}

func pipelineHash(item *layout.RegistryItem) uint64 {
	return registry.HashName(shm.Bytes(registry.Pipeline(item).Name[:]))
}
