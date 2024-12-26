#pragma once

#include "module.h"

#include "filter.h"

struct decap_module {
	struct module module;

	struct filter filter;
};

struct decap_module *
new_decap();
