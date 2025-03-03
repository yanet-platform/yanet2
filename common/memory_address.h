#pragma once

#define ADDR_OF(BASE, OFFSET)                                                  \
	((typeof(OFFSET))((uintptr_t)(BASE) + (uintptr_t)(OFFSET)))
#define OFFSET_OF(BASE, ADDR)                                                  \
	((typeof(ADDR))((uintptr_t)(ADDR) - (uintptr_t)(BASE)))
