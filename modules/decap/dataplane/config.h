#pragma once

#include "common/lpm.h"

#include "controlplane/config/zone.h"

struct decap_module_config {
	struct cp_module cp_module;

	struct lpm prefixes4;
	struct lpm prefixes6;
};
