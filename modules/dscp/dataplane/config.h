#pragma once

#include <stddef.h>

#include "common/lpm.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/dataplane/packet/dscp.h"

struct dscp_module_config {
	struct cp_module cp_module;

	struct lpm source_lpm_v4;
	struct lpm source_lpm_v6;
	struct lpm destination_lpm_v4;
	struct lpm destination_lpm_v6;
	uint8_t has_source_prefixes;
	uint8_t has_destination_prefixes;
	struct dscp_config dscp;
};

_Static_assert(
	offsetof(struct dscp_module_config, cp_module) == 0,
	"module configuration must start with the common header"
);
