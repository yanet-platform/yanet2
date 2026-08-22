package ffi

import "errors"

// ErrStillReferenced is reported by a handle Free when a live
// configuration generation still references the object: the object is
// intact, the free was refused, and the owner — the module, device or
// object control plane that knows the type — must remember the handle
// and free it again once the generations holding it drain.
var ErrStillReferenced = errors.New("object still referenced by a live configuration generation")
