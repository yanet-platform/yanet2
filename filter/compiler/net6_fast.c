#include "../classifiers/net6_fast.h"
#include "common/network.h"

static int
validate_net6(struct net6 *net6) {
    size_t first_zero = (size_t)-1;
    size_t last_zero = (size_t)-1;
    size_t zeroes_count = 0;
    for (size_t i = 0; i < 8 * NET6_LEN; ++i) {
        size_t byte = i / 8;
        size_t bit = (8 - i % 8) % 8;
        if (net6->addr[byte] & (1 << bit)) {
            if (first_zero == (size_t)-1) {
                first_zero = i;
            }
            last_zero = i;
            ++zeroes_count;
        }
    }
    if (first_zero == (size_t)-1) {
        return 1;
    }
    if (last_zero - first_zero + 1 != zeroes_count) {
        // non-consecutive zeroes
        return 0;
    }
    if (last_zero == NET6_LEN - 1) {
        return 1;
    }
    if (last_zero < 7) {
        return 0;
    }
    if (first_zero > 8) {
        return 0;
    }
    return 1;
}

static int
