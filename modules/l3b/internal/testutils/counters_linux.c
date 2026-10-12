#include "counters.h"

#include <stdlib.h>
#include <string.h>

#include "lib/controlplane/config/cp_counter.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "objects/l3b/api/l3b_session_table_object.h"
#include "objects/l3b/api/l3b_virtual_service_object.h"

struct ballast_block {
	void *pointer;
	size_t size;
};

static _Thread_local struct {
	int active;
	int exhaust_after_registration;
	char service[MEMORY_CONTEXT_NAME_SIZE];
	char counter[COUNTER_NAME_LEN];
	uint64_t size;
	struct l3b_test_counter_witness witness;
	struct l3b_test_allocation_witness allocation;
	struct block_allocator *allocator;
	struct ballast_block *blocks;
} fault;

// Reserve existing raw blocks without splitting or changing context accounting.
static void
reserve_arena(struct memory_context *context) {
	struct block_allocator *allocator = ADDR_OF(&context->block_allocator);
	if (allocator == NULL) {
		return;
	}
	spinlock_lock(&allocator->lock);
	size_t count = 0;
	for (size_t idx = 0; idx < MEMORY_BLOCK_ALLOCATOR_EXP; ++idx) {
		uint64_t available = allocator->pools[idx].free;
		if (available > SIZE_MAX / sizeof(*fault.blocks) - count) {
			goto unlock;
		}
		count += available;
	}
	if (count == 0) {
		goto unlock;
	}
	// Ordinary heap metadata is independent of the locked shared arena.
	fault.blocks = malloc(count * sizeof(*fault.blocks));
	if (fault.blocks == NULL) {
		goto unlock;
	}
	fault.allocator = allocator;
	for (size_t idx = 0; idx < MEMORY_BLOCK_ALLOCATOR_EXP; ++idx) {
		struct block_allocator_pool *pool = &allocator->pools[idx];
		size_t size = block_allocator_pool_size(allocator, idx);
		while (pool->free != 0) {
			fault.blocks[fault.allocation.reserved_blocks++] =
				(struct ballast_block){
					.pointer = block_allocator_pool_get(
						allocator, pool
					),
					.size = size,
				};
			fault.allocation.reserved_bytes += size;
		}
	}
	fault.allocation.exhausted = allocator->not_empty_mask == 0;
unlock:
	spinlock_unlock(&allocator->lock);
}

// Return reservations, preserving blocks returned by constructor cleanup.
static void
clear_fault(void) {
	if (fault.allocator != NULL) {
		spinlock_lock(&fault.allocator->lock);
		for (uint64_t idx = 0; idx < fault.allocation.reserved_blocks;
		     ++idx) {
			block_allocator_bfree_internal(
				fault.allocator,
				fault.blocks[idx].pointer,
				fault.blocks[idx].size
			);
		}
		spinlock_unlock(&fault.allocator->lock);
	}
	free(fault.blocks);
	memset(&fault, 0, sizeof(fault));
}

extern uint64_t
__real_counter_registry_register(
	struct counter_registry *, const char *, uint64_t, yanet_error **
);

uint64_t
__wrap_counter_registry_register(
	struct counter_registry *registry,
	const char *name,
	uint64_t size,
	yanet_error **err
) {
	struct memory_context *context = ADDR_OF(&registry->memory_context);
	if (fault.active && context != NULL &&
	    strcmp(context->name, fault.service) == 0 && size == fault.size &&
	    strcmp(name, fault.counter) == 0) {
		if (fault.exhaust_after_registration) {
			uint64_t counter_id = __real_counter_registry_register(
				registry, name, size, err
			);
			if (++fault.allocation.matches == 1 &&
			    counter_id != COUNTER_INVALID) {
				fault.allocation.registered_counters =
					registry->count;
				reserve_arena(context);
			}
			return counter_id;
		}
		++fault.witness.matches;
		if (fault.witness.failures == 0) {
			++fault.witness.failures;
			fault.witness.registered_before = registry->count;
			yanet_error_add(
				err, "injected counter registration refusal"
			);
			return COUNTER_INVALID;
		}
	}
	return __real_counter_registry_register(registry, name, size, err);
}

int
l3b_test_counter_fault_begin(
	const char *service, const char *counter, uint64_t size
) {
	if (fault.active || strlen(service) >= sizeof(fault.service) ||
	    strlen(counter) >= sizeof(fault.counter)) {
		return -1;
	}
	memset(&fault, 0, sizeof(fault));
	strcpy(fault.service, service);
	strcpy(fault.counter, counter);
	fault.size = size;
	fault.active = 1;
	return 0;
}

struct l3b_test_counter_witness
l3b_test_counter_fault_end(void) {
	struct l3b_test_counter_witness witness = fault.witness;
	clear_fault();
	return witness;
}

int
l3b_test_allocation_fault_begin(const char *service) {
	if (l3b_test_counter_fault_begin(service, "real/1", 2) != 0) {
		return -1;
	}
	fault.exhaust_after_registration = 1;
	return 0;
}

struct l3b_test_allocation_witness
l3b_test_allocation_fault_end(void) {
	struct l3b_test_allocation_witness witness = fault.allocation;
	clear_fault();
	return witness;
}

// Read settled definitions and their corresponding per-worker values.
static int
read_registry(
	struct counter_registry *registry,
	struct counter_storage *storage,
	int link,
	struct l3b_test_counter_state *out
) {
	if (storage == NULL) {
		return -1;
	}
	struct counter_registry *storage_registry = ADDR_OF(&storage->registry);
	struct counter *names = ADDR_OF(&registry->names);
	for (uint64_t idx = 0; idx < registry->count; ++idx) {
		struct counter *counter = names + idx;
		if (counter->gen != registry->gen) {
			continue;
		}
		if (out->count == L3B_TEST_COUNTER_CAPACITY ||
		    counter->size == 0 || counter->size > 2 ||
		    counter->offset == COUNTER_INVALID) {
			return -1;
		}
		uint64_t storage_idx = counter_registry_lookup_index(
			storage_registry, counter->name
		);
		if (storage_idx == COUNTER_INVALID) {
			return -1;
		}
		struct counter *stored =
			ADDR_OF(&storage_registry->names) + storage_idx;
		if (stored->size != counter->size ||
		    stored->offset != counter->offset) {
			return -1;
		}
		struct l3b_test_counter *item = &out->counters[out->count++];
		memcpy(item->name, counter->name, sizeof(item->name));
		item->size = counter->size;
		item->offset = counter->offset;
		item->link = link;
		uint64_t *values = counter_get_address(storage_idx, storage);
		for (uint64_t value_idx = 0; value_idx < item->size;
		     ++value_idx) {
			item->values[value_idx] = values[value_idx];
		}
	}
	return 0;
}

int
l3b_test_counter_state_read(
	uintptr_t ectx,
	const char *service,
	const struct l3b_test_counter_link *link,
	struct l3b_test_counter_state *out
) {
	memset(out, 0, sizeof(*out));
	struct config_gen_ectx *context = (struct config_gen_ectx *)ectx;
	if (context == NULL) {
		return -1;
	}
	struct cp_config_gen *generation = ADDR_OF(&context->cp_config_gen);
	uint64_t object_idx;
	if (cp_config_gen_lookup_object_index(
		    generation,
		    L3B_VIRTUAL_SERVICE_OBJECT_TYPE,
		    service,
		    &object_idx
	    ) ||
	    object_idx >= context->object_count) {
		return -1;
	}
	struct cp_object *object =
		cp_config_gen_get_object(generation, object_idx);
	struct object_ectx **objects = ADDR_OF(&context->objects);
	if (object == NULL || objects == NULL) {
		return -1;
	}
	struct object_ectx *object_context = ADDR_OF(objects + object_idx);
	if (object_context == NULL) {
		return -1;
	}
	struct counter_storage *object_storage =
		ADDR_OF(&object_context->counter_storage);
	struct counter_storage *link_storage =
		cp_config_counter_storage_registry_lookup_module_object_link(
			ADDR_OF(&context->counter_storage_registry),
			link->device,
			link->pipeline,
			link->function,
			link->chain,
			link->module_type,
			link->module,
			L3B_VIRTUAL_SERVICE_OBJECT_TYPE,
			service
		);
	struct l3b_session_table_object *table =
		l3b_virtual_service_session_table(object);
	if (table == NULL) {
		return -1;
	}
	out->service = (uintptr_t)object;
	out->table = (uintptr_t)table;
	out->table_references = table->cp_object.config_item.refcnt;
	out->object_storage = (uintptr_t)object_storage;
	out->link_storage = (uintptr_t)link_storage;
	if (read_registry(&object->counter_registry, object_storage, 0, out) ||
	    read_registry(
		    &object->link_counter_registry, link_storage, 1, out
	    )) {
		return -1;
	}
	return 0;
}
