#pragma once

#include "common/value.h"
#include "declare.h"

#include <stdint.h>

// The device attribute's runtime resolver.
//
// No compiled filter carries a device condition through the generic
// query path: a module that wants device-scoped rules resolves the
// device class itself, once per classification invocation, from the
// device its pipeline executes on — the one fact the invocation
// already knows. This getter is that resolution, shared by the
// module-local classifier wrappers.
//
// Returns the rule class of the given device, with the fallback class
// for a device beyond the compiled table.
static inline uint32_t
filter_attr_device_value(struct vline *line, uint32_t device_id) {
	if (device_id >= line->size) {
		device_id = 0;
	}
	return vline_get(line, device_id);
}
