#pragma once

#include "controlplane/config/zone.h"

#include "filter/ipfw.h"

// FIXME: make the structure private?
struct acl_module_config {
	struct cp_module cp_module;

	struct filter_compiler filter;
};
