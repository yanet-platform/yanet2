#include <errno.h>
#include <netinet/in.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "lib/dataplane/config/zone.h"
#include "modules/unrdup/api/controlplane.h"
#include "modules/unrdup/dataplane/config.h"
#include "modules/unrdup/dataplane/dataplane.h"

#include "common/strutils.h"
#include "lib/fuzzing/fuzzing.h"

static struct fuzzing_params fuzz_params = {0};

#define FUZZ_SERVICE_PORT 443

static const uint8_t fuzz_vip4[NET4_LEN] = {192, 0, 2, 1};
static const uint8_t fuzz_vip6[NET6_LEN] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
};

static const uint8_t fuzz_peer4[NET4_LEN] = {10, 0, 0, 10};
static const uint8_t fuzz_peer6[NET6_LEN] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0xb, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x11
};

static const uint8_t fuzz_source4[NET4_LEN] = {10, 0, 0, 1};
static const uint8_t fuzz_mask4[NET4_LEN] = {0xff, 0xff, 0xff, 0xfc};
static const uint8_t fuzz_source6[NET6_LEN] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0xa, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0
};
static const uint8_t fuzz_mask6[NET6_LEN] = {
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0,
	0,
	0,
	0
};

#define UNRDUP_FUZZ_ARENA_SIZE (1 << 24)

static void *unrdup_fuzz_arena;
static struct block_allocator unrdup_fuzz_ba;
static struct memory_context unrdup_fuzz_mctx;

static int
unrdup_fuzz_memory_init(void) {
	if (unrdup_fuzz_arena != NULL) {
		return 0;
	}

	unrdup_fuzz_arena = malloc(UNRDUP_FUZZ_ARENA_SIZE);
	if (unrdup_fuzz_arena == NULL) {
		return -ENOMEM;
	}

	block_allocator_init(&unrdup_fuzz_ba);
	block_allocator_put_arena(
		&unrdup_fuzz_ba, unrdup_fuzz_arena, UNRDUP_FUZZ_ARENA_SIZE
	);

	return memory_context_init(
		&unrdup_fuzz_mctx, "unrdup_fuzz", &unrdup_fuzz_ba
	);
}

static int
unrdup_test_config(struct cp_module **cp_module, yanet_error **err) {
	if (unrdup_fuzz_memory_init()) {
		return -ENOMEM;
	}

	struct unrdup_module_config *config =
		(struct unrdup_module_config *)memory_balloc(
			&unrdup_fuzz_mctx, sizeof(struct unrdup_module_config)
		);
	if (config == NULL) {
		return -ENOMEM;
	}

	memset(config, 0, sizeof(*config));

	strtcpy(config->cp_module.name,
		"unrdup_test",
		sizeof(config->cp_module.name));
	memory_context_init_from(
		&config->cp_module.memory_context,
		&unrdup_fuzz_mctx,
		"unrdup_test"
	);
	config->cp_module.dp_module_idx = 0;
	config->cp_module.agent = NULL;

	struct unrdup_peer_config peers[2] = {0};
	peers[0].family = ip_family_ip4;
	memcpy(peers[0].addr.v4.bytes, fuzz_peer4, NET4_LEN);
	peers[1].family = ip_family_ip6;
	memcpy(peers[1].addr.v6.bytes, fuzz_peer6, NET6_LEN);

	struct unrdup_port_config ports[2] = {
		{.port = FUZZ_SERVICE_PORT, .proto = IPPROTO_TCP},
		{.port = FUZZ_SERVICE_PORT, .proto = IPPROTO_UDP},
	};

	struct unrdup_service_config services[2] = {0};
	services[0].family = ip_family_ip4;
	memcpy(services[0].vip.v4.bytes, fuzz_vip4, NET4_LEN);
	services[1].family = ip_family_ip6;
	memcpy(services[1].vip.v6.bytes, fuzz_vip6, NET6_LEN);

	for (uint64_t idx = 0; idx < 2; ++idx) {
		services[idx].peers = peers;
		services[idx].peer_count = 2;
		services[idx].ports = ports;
		services[idx].port_count = 2;
	}

	if (unrdup_module_config_set_source(
		    &config->cp_module,
		    ip_family_ip4,
		    fuzz_source4,
		    fuzz_mask4,
		    err
	    )) {
		goto error;
	}

	if (unrdup_module_config_set_source(
		    &config->cp_module,
		    ip_family_ip6,
		    fuzz_source6,
		    fuzz_mask6,
		    err
	    )) {
		goto error;
	}

	if (unrdup_module_config_update_services(
		    &config->cp_module, services, 2, err
	    )) {
		goto error;
	}

	if (counter_registry_init(
		    &config->cp_module.counter_registry, &fuzz_params.mctx, 0
	    )) {
		goto free_services;
	}

	if (unrdup_module_config_register_counters(&config->cp_module, err)) {
		goto free_registry;
	}

	if (counter_registry_link(
		    &config->cp_module.counter_registry, NULL, err
	    )) {
		goto free_registry;
	}

	struct counter_storage *counter_storage = counter_storage_spawn(
		&fuzz_params.mctx, NULL, &config->cp_module.counter_registry
	);
	if (counter_storage == NULL) {
		goto free_registry;
	}
	SET_OFFSET_OF(
		&fuzz_params.module_ectx.counter_storage, counter_storage
	);

	*cp_module = &config->cp_module;
	return 0;

free_registry:
	counter_registry_fini(&config->cp_module.counter_registry);
free_services:
	unrdup_module_config_data_fini(config);
error:
	memory_bfree(
		&unrdup_fuzz_mctx, config, sizeof(struct unrdup_module_config)
	);
	return -EINVAL;
}

static int
fuzz_setup(yanet_error **err) {
	if (fuzzing_params_init(
		    &fuzz_params, "unrdup fuzzing", new_module_unrdup
	    ) != 0) {
		return EXIT_FAILURE;
	}

	fuzz_params.worker =
		memory_balloc(&fuzz_params.mctx, sizeof(struct dp_worker));
	if (fuzz_params.worker == NULL) {
		return EXIT_FAILURE;
	}
	memset(fuzz_params.worker, 0, sizeof(struct dp_worker));
	fuzz_params.worker->rx_mempool = fuzz_params.mempool;

	return unrdup_test_config(&fuzz_params.cp_module, err);
}

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	if (fuzz_params.module == NULL) {
		yanet_error *err = NULL;
		if (fuzz_setup(&err) != 0) {
			exit(1);
		}
	}

	return fuzzing_process_packet(&fuzz_params, data, size);
}
