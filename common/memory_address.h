#pragma once

#include <stdlib.h>

/**
 * @brief Convert a relative pointer to a virtual address
 *
 * A relative pointer P points to the virtual address (&P + P).
 * If P is NULL, it points to NULL as well.
 *
 * @param OFFSET The relative pointer to convert
 * @return Virtual address where OFFSET points to
 */
#define ADDR_OF(OFFSET)                                                        \
	__extension__({                                                        \
		typeof(*(OFFSET)) offset_val = *(OFFSET);                      \
		(typeof(offset_val))((uintptr_t)offset_val +                   \
				     (uintptr_t)((offset_val) ? (OFFSET)       \
							      : NULL));        \
	})

/**
 * @brief Convert a known-non-NULL relative pointer to a virtual address.
 *
 * Same as ADDR_OF but skips the NULL test. The compiler cannot prove the
 * stored offset is non-zero (it is data), so ADDR_OF always emits a branch /
 * cmov. On hot pointer-chases where the relative pointer is structurally never
 * NULL — e.g. an intermediate LPM trie node always has a child page — this
 * drops that per-hop cmov. If the offset is in fact 0 the result is the slot
 * address itself, a wrong value but never a crash.
 */
#define ADDR_OF_NONNULL(OFFSET)                                                \
	__extension__({                                                        \
		typeof(*(OFFSET)) offset_val = *(OFFSET);                      \
		(typeof(offset_val))((uintptr_t)offset_val +                   \
				     (uintptr_t)(OFFSET));                     \
	})

/**
 * @brief Set a relative pointer to point to a virtual address
 *
 * This macro sets a relative pointer to point to a specified virtual address.
 *
 * @param PTR Pointer to the relative pointer to be set
 * @param ADDR Virtual address that the relative pointer should point to
 *
 * @note After this macro is called, it is guaranteed that ADDR_OF(PTR) == ADDR.
 */
#define SET_OFFSET_OF(PTR, ADDR)                                               \
	do {                                                                   \
		typeof(ADDR) _addr = (ADDR);                                   \
		*(PTR) = (typeof(_addr))((uintptr_t)_addr -                    \
					 (uintptr_t)(_addr ? (PTR) : NULL));   \
	} while (0)

/**
 * @brief Assign one relative pointer to another
 *
 * This macro makes an assignment PTR1 = PTR2 in the sense of relative pointers.
 *
 * @param DST Pointer to the destination relative pointer
 * @param SRC Pointer to the source relative pointer
 *
 * @note After this macro is called, it is guaranteed that ADDR_OF(DST) ==
 * ADDR_OF(SRC).
 */
#define EQUATE_OFFSET(DST, SRC)                                                \
	do {                                                                   \
		SET_OFFSET_OF(DST, ADDR_OF(SRC));                              \
	} while (0)

#include <stdatomic.h>

#define ATOMIC_ADDR_OF(OFFSET)                                                 \
	__extension__({                                                        \
		typeof(*OFFSET) _offset = atomic_load_explicit(                \
			(_Atomic(typeof(*OFFSET)) *)OFFSET,                    \
			memory_order_acquire                                   \
		);                                                             \
		(typeof(_offset))((uintptr_t)_offset +                         \
				  (uintptr_t)(_offset ? (OFFSET) : NULL));     \
	})

#define ATOMIC_SET_OFFSET_OF(PTR, ADDR)                                        \
	do {                                                                   \
		typeof(ADDR) _addr = (ADDR);                                   \
		atomic_store_explicit(                                         \
			(_Atomic(typeof(*PTR)) *)PTR,                          \
			(typeof(_addr))((uintptr_t)_addr -                     \
					(uintptr_t)(_addr ? (PTR) : NULL)),    \
			memory_order_release                                   \
		);                                                             \
	} while (0)
