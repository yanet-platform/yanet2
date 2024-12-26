#pragma once

#ifndef container_of

#include <stddef.h>

#define container_of(ptr, type, member)                                        \
	({                                                                     \
		void *__mptr = (void *)(ptr);                                  \
		((type *)(__mptr - offsetof(type, member)));                   \
	})

#endif
