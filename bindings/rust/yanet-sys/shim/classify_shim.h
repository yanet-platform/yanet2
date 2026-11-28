// Constants of lib/classify, the classification library of the C modules.
//
// The Rust lookups read the classifiers the control plane builds; a lookup
// that matches no rule reports the invalid rule of the library.
#pragma once

// The rule index of a packet no rule matches, CLASSIFY_RULE_INVALID
// (lib/classify/classify.h).
#define YANET_SYS_CLASSIFY_NO_RULE 0xffffffffU
