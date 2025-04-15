#pragma once

#ifndef container_of

#include <stddef.h>

#define container_of(ptr, type, member)                                        \
	({                                                                     \
		void *mptr = (void *)(ptr);                                    \
		((type *)(mptr - offsetof(type, member)));                     \
	})

#endif
