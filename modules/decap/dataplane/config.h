#pragma once

#include "common/lpm.h"

#include "controlplane/config/zone.h"

struct decap_module_config {
	struct module_data module_data;

	struct lpm prefixes4;
	struct lpm prefixes6;
};
