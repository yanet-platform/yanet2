#pragma once

#include "common/lpm.h"

#include "dataplane/config/zone.h"

struct forward_module_config {
	struct module_data module_data;

	struct lpm lpm_v4;
	struct lpm lpm_v6;
};
