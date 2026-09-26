#pragma once

#include "common/value.h"

#include "lib/classify/classify.h"

#include <string.h>

/*
 * Class line over a small integer domain, embedded by value in a
 * consumer classifier; the line dies with the consumer through the
 * free below.
 *
 * One instantiation covers one attribute whose definition area is a
 * dense value domain - the vlan identifiers, the IP protocol numbers,
 * the transport specific byte beside its absent mark.
 */
struct classify_attr_line {
	struct vline line;
};

// Releases the internals of an embedded line classifier and zeroes it,
// so a destroy path is idempotent.
static inline void
classify_attr_line_free(
	struct memory_context *memory_context, struct classify_attr_line *attr
) {
	(void)memory_context;

	vline_free(&attr->line);
	memset(attr, 0, sizeof(*attr));
}
