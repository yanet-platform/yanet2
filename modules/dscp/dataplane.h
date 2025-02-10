#pragma once

#include "dataplane/module/module.h"
#include "dataplane/packet/dscp.h"
#include "lpm.h"

struct dscp_module_config {
	struct module_config config;

	struct lpm lpm_v4;
	struct lpm lpm_v6;
	struct dscp_config dscp;
};

struct module *
new_module_kernel();
