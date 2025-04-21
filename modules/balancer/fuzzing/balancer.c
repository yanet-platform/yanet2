#include <arpa/inet.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "dataplane/module/module.h"
#include "dataplane/module/testing.h"
#include "dataplane/packet/packet.h"
#include "modules/balancer/config.h"
#include "modules/balancer/controlplane.h"
#include "modules/balancer/dataplane.h"

#define ARENA_SIZE (1 << 20)
#define MBUF_MAX_SIZE 8196
#define RTE_PKTMBUF_HEADROOM 256

struct balancer_fuzzing_params {
	struct module *module; /**< Pointer to the module being tested */
	struct module_data *module_data; /**< Module configuration */

	void *arena;
	void *payload_arena;
	struct block_allocator ba;
	struct memory_context mctx;
};

static struct balancer_fuzzing_params fuzz_params = {
	.module_data = NULL,
};

void
parse_address(char *address, struct in6_addr *ret) {
	int rc = inet_pton(AF_INET6, address, ret);
	if (rc <= 0) {
		if (rc == 0) {
			fprintf(stderr, "Not in presentation format");
		} else {
			perror("inet_pton");
		}
		exit(EXIT_FAILURE);
	}
}

static int
balancer_test_config(struct module_data **module_data) {
	struct balancer_module_config *config =
		(struct balancer_module_config *)memory_balloc(
			&fuzz_params.mctx, sizeof(struct balancer_module_config)
		);

	if (!config) {
		return -ENOMEM;
	}

	// Initialize module_data fields
	strtcpy(config->module_data.name,
		"balancer_test",
		sizeof(config->module_data.name));
	memory_context_init_from(
		&config->module_data.memory_context,
		&fuzz_params.mctx,
		"balancer_test"
	);

	config->module_data.index = 0;
	config->module_data.agent = NULL;
	// FIXME:
	// config->module_data.free_handler = balancer_module_config_free;

	struct memory_context *memory_context =
		&config->module_data.memory_context;
	if (lpm_init(&config->v4_service_lookup, memory_context)) {
		goto error_lpm_v4;
	}
	if (lpm_init(&config->v6_service_lookup, memory_context)) {
		goto error_lpm_v6;
	}

	uint64_t real_count = 6;
	struct in6_addr address;
	parse_address("2a01:db8::853a:0:3", &address);
	struct balancer_service_config *svc_cfg =
		balancer_service_config_create(
			0x010002, address.s6_addr, real_count
		);

	struct in6_addr src_addr;
	parse_address("2a01:db8:6666::", &src_addr);
	struct in6_addr src_mask;
	parse_address("ffff:ffff:ffff:ffff:ffff:ffff::", &src_mask);
	// 1
	struct in6_addr dst_addr;
	parse_address("2a01:db8::675:a15a:3314", &dst_addr);
	balancer_service_config_set_real(
		svc_cfg,
		0,
		0x02,
		dst_addr.s6_addr,
		src_addr.s6_addr,
		src_mask.s6_addr
	);

	// 2
	parse_address("2a01:db8::675:a15a:3ca0", &dst_addr);
	balancer_service_config_set_real(
		svc_cfg,
		0,
		0x02,
		dst_addr.s6_addr,
		src_addr.s6_addr,
		src_mask.s6_addr
	);

	// 3
	parse_address("2a01:db8::675:a15a:4174", &dst_addr);
	balancer_service_config_set_real(
		svc_cfg,
		0,
		0x02,
		dst_addr.s6_addr,
		src_addr.s6_addr,
		src_mask.s6_addr
	);

	// 4
	parse_address("2a01:db8::675:a15a:4bb8", &dst_addr);
	balancer_service_config_set_real(
		svc_cfg,
		0,
		0x02,
		dst_addr.s6_addr,
		src_addr.s6_addr,
		src_mask.s6_addr
	);

	// 5
	parse_address("2a01:db8::675:a15a:4d6c", &dst_addr);
	balancer_service_config_set_real(
		svc_cfg,
		0,
		0x02,
		dst_addr.s6_addr,
		src_addr.s6_addr,
		src_mask.s6_addr
	);

	// 6
	parse_address("2a01:db8::675:a15a:0e98", &dst_addr);
	balancer_service_config_set_real(
		svc_cfg,
		0,
		0x02,
		dst_addr.s6_addr,
		src_addr.s6_addr,
		src_mask.s6_addr
	);

	int rc = balancer_module_config_add_service(
		&config->module_data, svc_cfg
	);
	balancer_service_config_free(svc_cfg); // free in anyway
	if (rc == 0) {			       // ok
		*module_data = (struct module_data *)config;
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

	return balancer_test_config(&fuzz_params.module_data);
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
	fuzz_params.module->handler(NULL, fuzz_params.module_data, pf);

	return 0;
}
