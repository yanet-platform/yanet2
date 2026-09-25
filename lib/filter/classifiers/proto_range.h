#pragma once

#include "common/value.h"

// Index into the unavailable-subtype classes, ordered to match the
// protocols whose classification reads a subtype byte from the transport
// header.
enum proto_unavailable_class {
	PROTO_UNAVAILABLE_TCP = 0,
	PROTO_UNAVAILABLE_ICMP = 1,
	PROTO_UNAVAILABLE_ICMPV6 = 2,
	PROTO_UNAVAILABLE_CLASS_COUNT,
};

struct proto_range_classifier {
	struct vline line;

	// Class IDs for packets whose declared protocol is known but whose
	// transport header is unavailable: a rule matches such a packet only
	// when its protocol ranges collectively cover the whole protocol
	// block. Zero until the compiler assigns the compacted class.
	uint32_t unavailable_classes[PROTO_UNAVAILABLE_CLASS_COUNT];
};
