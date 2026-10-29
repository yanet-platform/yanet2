//! The Miri layer of the runtime's unsafe code: the handler and commit
//! entries over a context laid out by hand, so that Miri executes what they
//! read from the mapping.
//!
//! Miri does not execute C, so this binary cannot run the dataplane. It tests
//! no module behaviour: verdicts, packet pools, counters and devices belong
//! to the Go tests over the C dataplane harness
//! (`bindings/go/dataplane_ut`). The packet allocator and copy of the
//! dataplane are not linked and no test reaches them.

mod common;
mod handler;
