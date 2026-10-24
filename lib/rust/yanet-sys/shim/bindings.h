// Headers the SDK binds through bindgen.
//
// The same repository headers the C dataplane compiles against; the build
// script feeds bindgen the include directories and defines of the meson
// pipeline library target.
#pragma once

#include "common/lpm.h"
