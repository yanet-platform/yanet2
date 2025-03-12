#pragma once

#define ADDR_OF(OFFSET)                                                        \
	((typeof(*OFFSET))(*(uintptr_t *)(OFFSET) +                            \
			   (uintptr_t)((*OFFSET) ? (OFFSET) : NULL)))
#define SET_OFFSET_OF(PTR, ADDR)                                               \
	do {                                                                   \
		*PTR = ((typeof(ADDR))((uintptr_t)(ADDR) -                     \
				       (uintptr_t)(ADDR ? PTR : NULL)));       \
	} while (0)
