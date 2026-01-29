#pragma once

#include "memory.h"
#include "memory_address.h"
#include "memory_block.h"
#include <stddef.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

struct big_array {
    void **subarrays;
    size_t subarrays_count;
    size_t subarray_len_exp;
    struct memory_context mctx;
};

static inline void
big_array_free(struct big_array *array);

static inline int
big_array_init(struct big_array *array, size_t size, struct memory_context *mctx) {
    memory_context_init_from(&array->mctx, mctx, "big_array");
    array->subarray_len_exp = 63 - __builtin_clzll(MEMORY_BLOCK_ALLOCATOR_MAX_SIZE);
    array->subarrays_count = (size + (1 << array->subarray_len_exp) - 1) >> array->subarray_len_exp;
    void **subarrays = memory_balloc(&array->mctx, sizeof(void *) * array->subarrays_count);
    if (subarrays == NULL) {
        goto free_on_error;
    }
    memset(subarrays, 0, sizeof(void *) * array->subarrays_count);
    SET_OFFSET_OF(&array->subarrays, subarrays);
    for (size_t i = 0; i < array->subarrays_count; ++i) {
        void *subarray = memory_balloc(&array->mctx, 1 << array->subarray_len_exp);
        if (subarray == NULL) {
            goto free_on_error;
        }
        SET_OFFSET_OF(subarrays + i, subarray);
    }
    return 0;

free_on_error:
    big_array_free(array);
    return -1;
}

static inline void *
big_array_get(struct big_array *array, size_t index) {
    size_t subarray_index = index >> array->subarray_len_exp;
    void **subarrays = ADDR_OF(&array->subarrays);
    return ADDR_OF(subarrays + subarray_index) + index;
}

static inline void
big_array_free(struct big_array *array) {
    if (array->subarrays == NULL) {
        return;
    }
    void **subarrays = ADDR_OF(&array->subarrays);
    for (size_t i = 0; i < array->subarrays_count; ++i) {
        void *subarray = ADDR_OF(subarrays + i);
        if (subarray != NULL) {
            memory_bfree(&array->mctx, subarray, 1 << array->subarray_len_exp);
            subarrays[i] = NULL;
        }
    }
    memory_bfree(&array->mctx, subarrays, sizeof(void *) * array->subarrays_count);
    memset(array, 0, sizeof(struct big_array));
}