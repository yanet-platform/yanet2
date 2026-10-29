package crud

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Module is a shared-memory module config under construction, ready to
// be published to the dataplane.
type Module interface {
	Free() error
	AsFFIModule() ffi.ModuleConfig
}

// Device is a shared-memory device config, ready to be published to
// the dataplane.
type Device interface {
	Free() error
	AsFFIDevice() ffi.ShmDeviceConfig
}

// PublishModule runs the build steps of a freshly created module config
// and publishes it to the dataplane.
//
// A nil build publishes the freshly created config as is. The abandoned
// config is freed when a build step or the publish fails, and a failed
// free is joined onto that step's own error.
func PublishModule[M Module](agent *ffi.Agent, module M, build func(module M) error) error {
	if build != nil {
		if err := build(module); err != nil {
			return abandonedModule(module, err)
		}
	}

	if err := agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		return abandonedModule(module, fmt.Errorf("failed to publish module config: %w", err))
	}

	return nil
}

// PublishDevice publishes a freshly built device config to the
// dataplane.
//
// The abandoned device is freed when the publish fails, and a failed
// free is joined onto the publish error.
func PublishDevice[D Device](agent *ffi.Agent, device D) error {
	if err := agent.UpdateDevices([]ffi.ShmDeviceConfig{device.AsFFIDevice()}); err != nil {
		return abandonedDevice(device, fmt.Errorf("failed to publish device config: %w", err))
	}

	return nil
}

// abandonedModule frees the abandoned config, joining a failed free
// onto the step's error.
func abandonedModule[M Module](module M, err error) error {
	if freeErr := module.Free(); freeErr != nil {
		return fmt.Errorf("%w; also failed to free the abandoned config: %w", err, freeErr)
	}
	return err
}

// abandonedDevice frees the abandoned device, joining a failed free
// onto the step's error.
func abandonedDevice[D Device](device D, err error) error {
	if freeErr := device.Free(); freeErr != nil {
		return fmt.Errorf("%w; also failed to free the abandoned config: %w", err, freeErr)
	}
	return err
}
