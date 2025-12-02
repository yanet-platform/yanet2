#include <stddef.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

int
run(void *arena,
    uint32_t workers_cnt,
    uint32_t capacity,
    uint32_t sessions,
    uint32_t iterations,
    uint32_t timeout_min,
    uint32_t timeout_max);