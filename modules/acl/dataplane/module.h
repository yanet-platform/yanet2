#pragma once

#include "filter/filter.h"
#include "controlplane/config/zone.h"

struct acl_module_config {
	struct cp_module cp_module;

	struct filter net4_filter;
	struct filter net6_filter;
};
