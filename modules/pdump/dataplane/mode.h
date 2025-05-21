#pragma once

enum pdump_mode {
	PDUMP_INPUT = 0b01, // NOLINT(readability-identifier-naming)
	PDUMP_DROPS = 0b10, // NOLINT(readability-identifier-naming)
	PDUMP_BOTH = 0b11,  // NOLINT(readability-identifier-naming)
};
