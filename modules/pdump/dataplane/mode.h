#pragma once

enum pdump_mode {
	PDUMP_INPUT = 1 << 0,	  // NOLINT(readability-identifier-naming)
	PDUMP_DROPS = 1 << 1,	  // NOLINT(readability-identifier-naming)
	PDUMP_ALL = (1 << 2) - 1, // NOLINT(readability-identifier-naming)
};
