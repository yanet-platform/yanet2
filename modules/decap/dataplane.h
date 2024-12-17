#pragma once

#include "dataplane/module/module.h"

#include "lpm.h"

struct decap_module_config {
	struct module_config config;

	struct lpm prefixes4;
	struct lpm prefixes6;
};

struct decap_module {
	struct module module;
};

// struct module *
// new_module_decap();
