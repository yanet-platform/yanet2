#pragma once

#include "common/lpm.h"
#include "controlplane/config/cp_module.h"
#include "dataplane/packet/dscp.h"

struct dscp_module_config {
	struct cp_module cp_module;

	struct lpm lpm_v4;
	struct lpm lpm_v6;
	struct dscp_config dscp;
};
