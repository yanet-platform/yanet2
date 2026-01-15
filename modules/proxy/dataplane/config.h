#pragma once

#include "controlplane/config/zone.h"

struct proxy_config {
    uint32_t addr;
};

struct proxy_module_config {
    struct cp_module cp_module;

    struct proxy_config proxy_config;
};