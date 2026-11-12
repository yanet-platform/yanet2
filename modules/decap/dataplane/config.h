#pragma once

#include "common/fast_lpm.h"
#include "lib/controlplane/config/cp_module.h"

struct decap_module_config {
	struct cp_module cp_module;

	struct fast_lpm prefixes4;
	struct fast_lpm prefixes6;
};
