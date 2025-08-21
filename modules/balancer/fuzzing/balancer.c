#include <arpa/inet.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "yanet_build_config.h" // MBUF_MAX_SIZE
#include <rte_build_config.h>	// RTE_PKTMBUF_HEADROOM

#include "dataplane/module/module.h"
#include "dataplane/module/testing.h"
#include "dataplane/packet/packet.h"
#include "modules/balancer/config.h"
#include "modules/balancer/controlplane.h"
#include "modules/balancer/dataplane.h"

#define ARENA_SIZE (1 << 20)

struct balancer_fuzzing_params {
	struct module *module;	     /**< Pointer to the module being tested */
	struct cp_module *cp_module; /**< Module configuration */

	void *arena;
	void *payload_arena;
	struct block_allocator ba;
	struct memory_context mctx;
};

static struct balancer_fuzzing_params fuzz_params = {
	.cp_module = NULL,
};

void
parse_address(int af, char *address, void *ret) {
	int rc = inet_pton(af, address, ret);
	if (rc <= 0) {
		if (rc == 0) {
			fprintf(stderr, "ERR: Not in presentation format\n");
		} else {
			perror("inet_pton");
		}
		exit(EXIT_FAILURE);
	}
}

static void
add_real_servers(
	struct balancer_service_config *svc_cfg,
	char *real_addresses[],
	uint64_t real_count,
	struct in_addr src_addr_v4,
	struct in6_addr src_addr_v6,
	struct in_addr src_mask_v4,
	struct in6_addr src_mask_v6
) {
	for (uint64_t real_idx = 0; real_idx < real_count; real_idx++) {
		struct in6_addr dst6_addr;
		struct in_addr dst4_addr;
		char *ip = real_addresses[real_idx];
		uint8_t *addr;
		int is_v6 = strchr(ip, ':') != NULL;

		if (is_v6) {
			parse_address(
				AF_INET6, real_addresses[real_idx], &dst6_addr
			);
			addr = dst6_addr.s6_addr;
		} else {
			parse_address(
				AF_INET, real_addresses[real_idx], &dst4_addr
			);
			addr = (uint8_t *)&dst4_addr.s_addr;
		}
		balancer_service_config_set_real(
			svc_cfg,
			real_idx,
			is_v6 ? RS_TYPE_V6 : RS_TYPE_V4,
			1,
			addr,
			is_v6 ? src_addr_v6.s6_addr
			      : (uint8_t *)&src_addr_v4.s_addr,
			is_v6 ? src_mask_v6.s6_addr
			      : (uint8_t *)&src_mask_v4.s_addr
		);
	}
}

static int
balancer_test_config(struct cp_module **cp_module) {
	struct balancer_module_config *config =
		(struct balancer_module_config *)memory_balloc(
			&fuzz_params.mctx, sizeof(struct balancer_module_config)
		);

	if (!config) {
		return -ENOMEM;
	}

	// Initialize cp_module fields
	strtcpy(config->cp_module.name,
		"balancer_test",
		sizeof(config->cp_module.name));
	memory_context_init_from(
		&config->cp_module.memory_context,
		&fuzz_params.mctx,
		"balancer_test"
	);

	config->cp_module.type = 0;
	config->cp_module.agent = NULL;
	// FIXME:
	// config->cp_module.free_handler = balancer_module_config_free;

	struct memory_context *memory_context =
		&config->cp_module.memory_context;
	if (lpm_init(&config->v4_service_lookup, memory_context)) {
		goto error_lpm_v4;
	}
	if (lpm_init(&config->v6_service_lookup, memory_context)) {
		goto error_lpm_v6;
	}

	struct in_addr src_addr_v4;
	parse_address(AF_INET, "10.6.0.0", &src_addr_v4);
	struct in_addr src_mask_v4;
	parse_address(AF_INET, "255.255.255.0", &src_mask_v4);
	struct in6_addr src_addr_v6;
	parse_address(AF_INET6, "2a01:db8:6666::", &src_addr_v6);
	struct in6_addr src_mask_v6;
	parse_address(
		AF_INET6, "ffff:ffff:ffff:ffff:ffff:ffff::", &src_mask_v6
	);

	char *real_addresses[6] = {
		"2a01:db8::675:a15a:3314",
		"2a01:db8::675:a15a:3ca0",
		"2a01:db8::675:a15a:4174",
		"192.168.1.1",
		"192.168.1.2",
		"192.168.1.3"
	};
	uint64_t real_count = 6;

	// IPv6 service configuration
	struct in6_addr address;
	parse_address(AF_INET6, "2a01:db8::853a:0:3", &address);
	struct balancer_service_config *svc_cfg_ipv6 =
		balancer_service_config_create(
			VS_OPT_ENCAP | VS_TYPE_V6,
			address.s6_addr,
			real_count,
			1
		);
	add_real_servers(
		svc_cfg_ipv6,
		real_addresses,
		real_count,
		src_addr_v4,
		src_addr_v6,
		src_mask_v4,
		src_mask_v6
	);

	// 2a01::0/96
	balancer_service_config_set_src_prefix(
		svc_cfg_ipv6,
		0,
		(uint8_t[16]){0x2a, 0x01, [15] = 0},
		(uint8_t[16]
		){0x2a, 0x01, [12] = 0xff, [13] = 0xff, [14] = 0xff, [15] = 0xff
		}
	);

	int rc = balancer_module_config_add_service(
		&config->cp_module, svc_cfg_ipv6
	);
	balancer_service_config_free(svc_cfg_ipv6); // free in anyway
	if (rc != 0) {
		goto error_lpm_v6;
	}

	// IPv4 service configuration
	struct in_addr address_v4;
	parse_address(AF_INET, "10.10.10.10", &address_v4);
	struct balancer_service_config *svc_cfg_ipv4 =
		balancer_service_config_create(
			VS_OPT_ENCAP | VS_TYPE_V4,
			(uint8_t *)&address_v4.s_addr,
			real_count,
			1
		);
	add_real_servers(
		svc_cfg_ipv4,
		real_addresses,
		real_count,
		src_addr_v4,
		src_addr_v6,
		src_mask_v4,
		src_mask_v6
	);

	// 10.6.0.0/16
	balancer_service_config_set_src_prefix(
		svc_cfg_ipv6,
		0,
		(uint8_t[16]){10, 6},
		(uint8_t[16]){10, 6, 0xff, 0xff}
	);

	rc = balancer_module_config_add_service(
		&config->cp_module, svc_cfg_ipv4
	);
	balancer_service_config_free(svc_cfg_ipv4); // free in anyway
	if (rc != 0) {
		goto error_lpm_v6;
	}

	if (rc == 0) { // ok
		*cp_module = (struct cp_module *)config;
		return 0;
	}

error_lpm_v6:
	lpm_free(&config->v4_service_lookup);

error_lpm_v4:
	memory_bfree(
		&fuzz_params.mctx, config, sizeof(struct balancer_module_config)
	);
	return -EINVAL;
}

static int
fuzz_setup() {
	fuzz_params.arena = malloc(ARENA_SIZE);
	if (fuzz_params.arena == NULL) {
		return EXIT_FAILURE;
	}

	block_allocator_init(&fuzz_params.ba);
	block_allocator_put_arena(
		&fuzz_params.ba, fuzz_params.arena, ARENA_SIZE
	);

	memory_context_init(
		&fuzz_params.mctx, "balancer fuzzing", &fuzz_params.ba
	);

	fuzz_params.module = new_module_balancer();
	fuzz_params.payload_arena = memory_balloc(
		&fuzz_params.mctx,
		sizeof(struct packet_front) + MBUF_MAX_SIZE * 4
	);
	if (fuzz_params.payload_arena == NULL) {
		return -ENOMEM;
	}

	return balancer_test_config(&fuzz_params.cp_module);
}

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	if (fuzz_params.module == NULL) {
		if (fuzz_setup() != 0) {
			exit(1); // Proper setup is essential for continuing
		}
	}

	if (size > (MBUF_MAX_SIZE - RTE_PKTMBUF_HEADROOM)) {
		return 0;
	}
	struct test_data payload[] = {{.payload = data, .size = size}};

	struct packet_front *pf = testing_packet_front(
		payload,
		fuzz_params.payload_arena,
		sizeof(struct packet_front) + MBUF_MAX_SIZE * 4,
		1,
		MBUF_MAX_SIZE
	);

	parse_packet(pf->input.first);
	// Process packet through balancer module
	fuzz_params.module->handler(NULL, 0, fuzz_params.cp_module, NULL, pf);

	return 0;
}
