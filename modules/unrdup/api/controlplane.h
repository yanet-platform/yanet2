#pragma once

#include <stdint.h>

#include "common/network.h"

#include "lib/errors/errors.h"

struct agent;
struct cp_module;
struct unrdup_module_config;

struct cp_module *
unrdup_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
);

void
unrdup_module_config_free(struct cp_module *cp_module);

void
unrdup_module_config_data_fini(struct unrdup_module_config *config);

int
unrdup_module_config_register_counters(
	struct cp_module *cp_module, yanet_error **err
);

// addr and mask are in network byte order, sized by family.
int
unrdup_module_config_set_source(
	struct cp_module *cp_module,
	enum ip_family family,
	const uint8_t *addr,
	const uint8_t *mask,
	yanet_error **err
);

struct unrdup_peer_config {
	struct net_addr addr;
	enum ip_family family;
};

// The port is in host byte order; proto is an IPPROTO_* value.
struct unrdup_port_config {
	uint16_t port;
	uint8_t proto;
};

struct unrdup_service_config {
	struct net_addr vip;
	enum ip_family family;

	const struct unrdup_peer_config *peers;
	uint64_t peer_count;

	const struct unrdup_port_config *ports;
	uint64_t port_count;
};

// A failure leaves the module on its previous configuration.
int
unrdup_module_config_update_services(
	struct cp_module *cp_module,
	const struct unrdup_service_config *services,
	uint64_t service_count,
	yanet_error **err
);
