#pragma once

#include <stdbool.h>

#include "asan.h"

// Reports whether this translation unit was compiled as a production build:
// optimizations enabled and neither AddressSanitizer nor a Meson-selected
// sanitizer enabled.
//
// The answer reflects the compile flags of whichever translation unit
// includes this header, so it is only meaningful from a meson-built C
// source and must never be evaluated from a cgo preamble, which compiles
// with its own, independent flags.
static inline bool
build_is_optimized(void) {
#if defined(HAVE_ASAN) || defined(YANET_SANITIZE)
	return false;
#elif defined(__OPTIMIZE__)
	return true;
#else
	return false;
#endif
}
