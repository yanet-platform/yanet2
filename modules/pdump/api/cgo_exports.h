#pragma once

#include <stdint.h>

extern void
pdumpGoControlplaneLog( // NOLINT(readability-identifier-naming)
	uint32_t level,
	const char *msg
);

// https://github.com/golang/go/wiki/cgo#function-variables
extern void
goErrorCallback( // NOLINT(readability-identifier-naming)
	uintptr_t h,
	char *msg
);
