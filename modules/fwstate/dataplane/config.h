#pragma once

#define FWSTATE_MODULE_NAME "fwstate"

#include "controlplane/config/zone.h"

#include "fwstate/config.h"

struct fwstate_module_config {
	struct cp_module cp_module;

	struct fwstate_config cfg;
};
