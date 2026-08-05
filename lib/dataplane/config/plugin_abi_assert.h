#pragma once

// Size tripwires for the structs a module .so exchanges with the dataplane
// binary across the plugin boundary.
//
// Both the dataplane binary and every plugin .so compile these assertions,
// so a layout change on either side of the boundary fails the build unless
// YANET_MODULE_ABI_VERSION in module.h is bumped to match.
//
// This is a size check only: it catches a struct that grew or shrank, not a
// same-size field reshuffle, so a reviewer must still bump the version on any
// layout change that keeps the size constant. When one of these fires, update
// the literal here and bump YANET_MODULE_ABI_VERSION in the same change.

#include "lib/controlplane/config/cp_module.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"

_Static_assert(
	sizeof(struct module) == 88,
	"struct module size changed, bump YANET_MODULE_ABI_VERSION"
);
_Static_assert(
	sizeof(struct dp_config) == 1184,
	"struct dp_config size changed, bump YANET_MODULE_ABI_VERSION"
);
_Static_assert(
	sizeof(struct packet) == 48,
	"struct packet size changed, bump YANET_MODULE_ABI_VERSION"
);
_Static_assert(
	sizeof(struct packet_front) == 160,
	"struct packet_front size changed, bump YANET_MODULE_ABI_VERSION"
);
_Static_assert(
	sizeof(struct cp_module) == 552,
	"struct cp_module size changed, bump YANET_MODULE_ABI_VERSION"
);
_Static_assert(
	sizeof(struct module_ectx) == 152,
	"struct module_ectx size changed, bump YANET_MODULE_ABI_VERSION"
);
_Static_assert(
	sizeof(struct config_gen_ectx) == 208,
	"struct config_gen_ectx size changed, bump YANET_MODULE_ABI_VERSION"
);
_Static_assert(
	sizeof(struct dp_worker) == 160,
	"struct dp_worker size changed, bump YANET_MODULE_ABI_VERSION"
);
_Static_assert(
	sizeof(struct dp_port) == 120,
	"struct dp_port size changed, bump YANET_MODULE_ABI_VERSION"
);
