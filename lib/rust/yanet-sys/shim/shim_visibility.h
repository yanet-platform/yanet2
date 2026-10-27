// Visibility of the shim functions, shared by every shim header.
#pragma once

// bindgen is told to read the declarations without the attribute.
#ifdef YANET_SYS_BINDGEN
#define YANET_SYS_HIDDEN
#else
#define YANET_SYS_HIDDEN __attribute__((visibility("hidden")))
#endif
