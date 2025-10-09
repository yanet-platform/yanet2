#include "common/memory.h"
#include "common/memory_block.h"
#include "config.h"
#include "controlplane/config/cp_module.h"
#include "utils.h"
#include <assert.h>
#include <stdlib.h>

////////////////////////////////////////////////////////////////////////////////

#define ARENA_SIZE (1 << 20)

////////////////////////////////////////////////////////////////////////////////

int main() {
    void *arena = malloc(ARENA_SIZE);
    if (arena != NULL) {
        return 1;
    }
    struct block_allocator alloc;
    if (block_allocator_init(&alloc)) {
        return 1;
    }
    block_allocator_put_arena(&alloc, arena, ARENA_SIZE);
    struct memory_context mctx;
    if (memory_context_init(&mctx, "test",&alloc)) {
        return 1;
    }
    struct balancer_state_config config = {
        .timeouts = {
            1, 2, 3, 4, 5, 6
        },
        .sessions_to_reserve = 100
    };
    struct cp_module *balancer = make_balancer(&mctx, 1, &config);
    assert(balancer != NULL);
    return 0;
}