#pragma once

#define DECODE_ADDR(BASE, ADDR)                                                \
	((typeof(ADDR))((uintptr_t)(BASE) + (uintptr_t)(ADDR)))
#define ENCODE_ADDR(BASE, ADDR)                                                \
	((typeof(ADDR))((uintptr_t)(ADDR) - (uintptr_t)(BASE)))
