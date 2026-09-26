#pragma once

#include "common/value.h"

#include "lib/classify/classify.h"

#include <string.h>

/*
 * Port classifier: the class line over the 16 bit port domain.
 *
 * The struct is embedded by value in a consumer classifier; the line
 * dies with the consumer through the free below.
 */
struct classify_attr_port {
	struct vline line;
};

// Releases the internals of an embedded port classifier and zeroes it,
// so a destroy path is idempotent.
static inline void
classify_attr_port_free(
	struct memory_context *memory_context, struct classify_attr_port *attr
) {
	(void)memory_context;

	vline_free(&attr->line);
	memset(attr, 0, sizeof(*attr));
}
