#include "common/memory.h"
#include "common/memory_block.h"
#include "controlplane/config/cp_module.h"
#include "session.h"
#include <assert.h>
#include <stdlib.h>

#include "controlplane.h"

#include "../utils/balancer.h"

////////////////////////////////////////////////////////////////////////////////

#define ARENA_SIZE (1 << 25)

////////////////////////////////////////////////////////////////////////////////

int
main() {
	void *arena = malloc(ARENA_SIZE);
	if (arena == NULL) {
		return 1;
	}
	struct block_allocator alloc;
	if (block_allocator_init(&alloc)) {
		return 1;
	}
	block_allocator_put_arena(&alloc, arena, ARENA_SIZE);
	struct memory_context mctx;
	if (memory_context_init(&mctx, "test", &alloc)) {
		return 1;
	}
	struct balancer_session_timeouts timeouts = {1, 2, 3, 4, 5, 6};
	struct balancer_state *state = make_balancer_state(&mctx, 1, 100);
	struct cp_module *balancer = make_balancer(&mctx, &timeouts, state);
	assert(balancer != NULL);
	uint8_t addr[4] = {1, 1, 1, 1};
	struct balancer_service_config *service =
		balancer_service_config_create(0, addr, 80, IPPROTO_TCP, 2, 1);
	uint8_t real_addr[4] = {2, 2, 2, 2};
	uint8_t src_addr[4] = {5, 1, 2, 3};
	uint8_t src_mask[4] = {2, 255, 255, 1};
	uint8_t real_addr1[4] = {3, 3, 3, 3};
	balancer_service_config_set_real(
		service, 0, 0, 1, real_addr, src_addr, src_mask
	);
	balancer_service_config_set_real(
		service, 1, 0, 1, real_addr1, src_addr, src_mask
	);
	uint8_t start_addr[4] = {3, 2, 1, 0};
	uint8_t end_addr[4] = {5, 0, 1, 2};
	balancer_service_config_set_src_prefix(
		service, 0, start_addr, end_addr
	);
	free(arena);
	return 0;
}