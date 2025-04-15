#pragma once

#include "common/lpm.h"
#include "dataplane/config/zone.h"
#include "dataplane/module/module.h"
#include "dataplane/packet/dscp.h"

struct dscp_module_config {
	struct module_data module_data;

	struct lpm lpm_v4;
	struct lpm lpm_v6;
	struct dscp_config dscp;
};
