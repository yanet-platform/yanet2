#pragma once

#include "controlplane/config/zone.h"

struct ipv4_prefix {
    uint32_t addr;
    uint8_t mask;
};

struct proxy_config {
    uint32_t upstream_addr;
    uint16_t upstream_port;

    uint32_t proxy_addr;
    uint16_t proxy_port;

    struct ipv4_prefix upstream_net;

    uint32_t size_connections_table;
};

struct proxy_module_config {
    struct cp_module cp_module;

    struct proxy_config proxy_config;
};