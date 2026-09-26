/**
 * @file rule.h
 * @brief Attribute value views the classifiers compile from.
 *
 * The library is rule format agnostic: a ruleset is an array of module
 * defined rules, and every attribute compiler asks for its values
 * through a module authored getter that fills one of the shared value
 * views (common/filter_views.h). No library owned rule structure
 * exists, and the modules compile their own rule arrays directly; the
 * classifier base the ruleset is addressed through and the class and
 * group marks live in classify.h.
 *
 * The line interval view below is the one exception to the stored
 * field views: the domain intervals of a line attribute are derived
 * from the module rule fields by the control plane that authors the
 * rules, so the rule carries the derived intervals itself and the
 * getter hands the view out.
 */

#pragma once

#include <stdint.h>

#include "common/filter_views.h"

// One interval of a line attribute domain, inclusive bounds.
struct classify_line_range {
	uint32_t from;
	uint32_t to;
};

// The derived domain intervals of one rule: a view over the intervals
// the rule storage owns, filled by the module getter.
struct classify_line_ranges {
	const struct classify_line_range *items;
	uint32_t count;
};
