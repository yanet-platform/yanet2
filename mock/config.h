#pragma once

#include <stddef.h>

////////////////////////////////////////////////////////////////////////////////

#define YANET_MOCK_MAX_DEVICES 8

////////////////////////////////////////////////////////////////////////////////

/// Config of the device
struct yanet_mock_device_config {
	size_t id;
	char name[80];
};

/// Config of the yanet mock
struct yanet_mock_config {
	/// Controlplane memory
	size_t cp_memory;

	// Dataplane memory
	size_t dp_memory;

	/// Number of workers
	size_t worker_count;

	/// Number of devices
	size_t device_count;

	/// Devices config
	struct yanet_mock_device_config devices[YANET_MOCK_MAX_DEVICES];
};