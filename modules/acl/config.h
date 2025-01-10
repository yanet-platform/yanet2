#pragma once

#include "dataplane/config/zone.h"

#include "ipfw.h"

// FIXME: make the structure private?
struct acl_module_config {
	struct module_data module_data;

	struct filter_compiler filter;
};
