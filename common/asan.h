#pragma once

#include <stddef.h>
#if defined(__has_feature)
#if __has_feature(address_sanitizer)
#define HAVE_ASAN 1
#endif
#endif

#if !defined(HAVE_ASAN)
#if defined(__SANITIZE_ADDRESS__)
#define HAVE_ASAN 1
#endif
#endif

#ifdef HAVE_ASAN

#include <sanitizer/asan_interface.h>

static inline void
asan_poison_memory_region(void *addr, size_t size) {
	__asan_poison_memory_region(addr, size);
}
static inline void
asan_unpoison_memory_region(void *addr, size_t size) {
	__asan_unpoison_memory_region(addr, size);
}

#else

static inline void
asan_poison_memory_region(void *addr, size_t size) {
	(void)addr;
	(void)size;
}
static inline void
asan_unpoison_memory_region(void *addr, size_t size) {
	(void)addr;
	(void)size;
}

#endif