#pragma once

#include "generic/rte_spinlock.h"
#include <stdalign.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <memory.h>

#include <rte_hash_crc.h>
#include <rte_spinlock.h>

////////////////////////////////////////////////////////////////////////////////

#define define_chain(key_type, value_type) alignas(64) struct chain_t { \
    key_type key;\
    value_type value; \
    uint8_t occupied; \
} \

#define chain_key_offset(key_type, value_type) offsetof(define_chain(key_type, value_type), key)
#define chain_value_offset(key_type, value_type) offsetof(define_chain(key_type, value_type), value)
#define chain_occupied_offset(key_type, value_type) offsetof(define_chain(key_type, value_type), occupied)
#define chain_alignment(key_type, value_type) alignof(define_chain(key_type, value_type))

////////////////////////////////////////////////////////////////////////////////


// #define furrytable_lookup(table_ptr, key_ptr, value_ptr)

typedef struct furrytable {
    void **chains; // every chain[] has size 64MB
    size_t chain_count; // 2^k
} furrytable_t;

void furrytable_lookup(struct furrytable *table, void *key, void **value, rte_spinlock_t **lock) {

}

int main() {
    struct furrytable *table = NULL;
    int *key;
    int **value;
    typedef typeof(*key) key_type;
    typedef typeof(**value) value_type;
    typedef struct chain {
        key_type key;
        value_type value;
        uint8_t occupied;
        rte_spinlock_t lock;
    } chain_t;
    uint32_t hash = rte_hash_crc(&key, sizeof(key_type), 0);
    uint32_t chain_id = hash & (table->chain_count - 1);
    return 0;
}

////////////////////////////////////////////////////////////////////////////////