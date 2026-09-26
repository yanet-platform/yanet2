#pragma once

#include "common/value.h"

#include "lib/classify/classify.h"

#include <stdint.h>
#include <string.h>

/*
 * Device classifier: the class line indexed by the module device
 * identifier.
 *
 * The struct is embedded by value in a consumer classifier; the class
 * line dies with the consumer through the free below.
 */
struct classify_attr_device {
	struct vline line;
};

// Releases the internals of an embedded device classifier and zeroes
// it, so a destroy path is idempotent.
static inline void
classify_attr_device_free(
	struct memory_context *memory_context, struct classify_attr_device *attr
) {
	(void)memory_context;

	vline_free(&attr->line);
	memset(attr, 0, sizeof(*attr));
}
