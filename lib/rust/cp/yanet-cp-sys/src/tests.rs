//! Shared-memory walking and C-ABI helpers, run natively and under Miri.
//!
//! A fixture arena stands in for the shared mapping: an agent whose arena
//! table points back into the same allocation, laid out at the offsets a test
//! layout names, as the C layout would.

use core::{alloc::Layout as AllocLayout, ffi::c_char, marker::PhantomData, ptr::NonNull};

use super::{Agent, DeviceBlock, Layout, abi, raw};

const ARENA_SIZE: usize = 8192;
const TABLE_OFFSET: usize = 256;
const DATA_OFFSET: usize = 1024;
const DATA_LEN: usize = 4096;

/// A layout with the agent's arena count at 0 and its table slot at 8, and
/// two-word arena entries.
fn layout() -> Layout {
    Layout(raw::Layout {
        cp_device_size: 64,
        cp_device_align: 8,
        cp_device_agent: 0,
        cp_device_input: 8,
        cp_device_output: 16,
        agent_arena_count: 0,
        agent_arenas: 8,
        arena_size: 16,
        arena_data: 0,
        arena_len: 8,
        device_name_len: 80,
        pipeline_name_len: 80,
    })
}

/// A zeroed arena standing in for the mapping, freed on drop.
struct Arena {
    base: NonNull<u8>,
}

impl Arena {
    fn alloc_layout() -> AllocLayout {
        AllocLayout::from_size_align(ARENA_SIZE, 64).expect("valid arena layout")
    }

    /// An agent at 0 with one arena of DATA_LEN bytes at DATA_OFFSET.
    fn with_agent() -> Self {
        // SAFETY: the layout has a nonzero size.
        let base = unsafe { std::alloc::alloc_zeroed(Self::alloc_layout()) };
        let arena = Self {
            base: NonNull::new(base).expect("arena allocation"),
        };
        arena.write_u64(0, 1);
        arena.write_u64(8, (TABLE_OFFSET - 8) as u64);
        arena.write_u64(TABLE_OFFSET, (DATA_OFFSET - TABLE_OFFSET) as u64);
        arena.write_u64(TABLE_OFFSET + 8, DATA_LEN as u64);
        arena
    }

    fn addr(&self, offset: usize) -> usize {
        self.base.addr().get() + offset
    }

    fn write_u64(&self, offset: usize, value: u64) {
        assert!(offset + 8 <= ARENA_SIZE);
        // SAFETY: in bounds and aligned.
        unsafe { self.base.add(offset).cast::<u64>().write(value) };
    }

    /// The fixture agent, with the test layout.
    fn agent(&self) -> Agent {
        Agent {
            root: self.base,
            layout: layout(),
            shim: false,
        }
    }

    /// An unpublished block whose body starts at its first byte.
    fn block(&self, offset: usize, size: usize) -> DeviceBlock<'_> {
        // SAFETY: in bounds of the fixture.
        let root = unsafe { self.base.add(offset) };
        DeviceBlock {
            root,
            size,
            body_offset: 0,
            _agent: PhantomData,
        }
    }
}

impl Drop for Arena {
    fn drop(&mut self) {
        // SAFETY: allocated in the constructor with the same layout.
        unsafe { std::alloc::dealloc(self.base.as_ptr(), Self::alloc_layout()) };
    }
}

#[test]
fn test_agent_arenas_follow_relative_table() {
    let arena = Arena::with_agent();

    let arenas = arena.agent().arenas();

    assert_eq!(
        vec![arena.addr(DATA_OFFSET)..arena.addr(DATA_OFFSET + DATA_LEN)],
        arenas
    );
}

#[test]
fn test_validator_over_agent_arenas_bounds_values() {
    let arena = Arena::with_agent();
    let agent = arena.agent();
    let checks = agent.validate(|validator| {
        [
            validator.check_range(arena.addr(DATA_OFFSET), 64, 8).is_ok(),
            validator
                .check_range(arena.addr(DATA_OFFSET + DATA_LEN - 8), 16, 8)
                .is_ok(),
            validator.check_range(arena.addr(0), 8, 8).is_ok(),
        ]
    });

    assert_eq!([true, false, false], checks);
}

#[test]
fn test_block_write_and_rel_target() {
    let arena = Arena::with_agent();
    let mut block = arena.block(DATA_OFFSET, 128);
    block.write::<u64>(8, 32).expect("in-bounds write");

    assert_eq!(Ok(Some(arena.addr(DATA_OFFSET + 40))), block.rel_target(8));
    assert_eq!(Ok(None), block.rel_target(16));
}

#[test]
fn test_block_write_rejects_header_bytes() {
    let arena = Arena::with_agent();
    let mut block = arena.block(DATA_OFFSET, 128);
    block.body_offset = 64;

    assert!(block.write::<u64>(56, 1).is_err());
    assert!(block.write::<u64>(64, 1).is_ok());
    assert!(block.read::<u64>(0).is_err());
}

#[test]
fn test_block_from_raw_gives_no_body_access() {
    let arena = Arena::with_agent();
    // SAFETY: an in-bounds region of the fixture.
    let mut block =
        unsafe { DeviceBlock::from_raw(arena.base.add(DATA_OFFSET).as_ptr().cast(), 128) }.expect("non-null block");

    assert!(block.write::<u64>(64, 1).is_err());
    assert!(block.read::<u64>(64).is_err());
    assert_eq!(Ok(None), block.rel_target(0));
}

#[test]
fn test_block_write_rejects_out_of_bounds_and_misaligned() {
    let arena = Arena::with_agent();
    let mut block = arena.block(DATA_OFFSET, 128);

    assert!(block.write::<u64>(124, 1).is_err());
    assert!(block.write::<u64>(4, 1).is_err());
    assert!(block.rel_target(128).is_err());
}

/// Calls the guard with a body and returns the code and message.
fn run_guard(body: impl FnOnce() -> Result<(), abi::Failure>) -> (i32, String) {
    let mut buf = [0 as c_char; 16];
    // SAFETY: the buffer is writable for its length.
    let code = unsafe { abi::guard(buf.as_mut_ptr(), buf.len(), body) };
    // SAFETY: the guard always terminates the message.
    let message = unsafe { core::ffi::CStr::from_ptr(buf.as_ptr()) };
    (code, message.to_string_lossy().into_owned())
}

#[test]
fn test_guard_reports_success() {
    assert_eq!((abi::OK, String::new()), run_guard(|| Ok(())));
}

#[test]
fn test_guard_truncates_failure_message() {
    let (code, message) = run_guard(|| Err(abi::Failure::new(abi::NOT_FOUND, "a message longer than the buffer")));

    assert_eq!(abi::NOT_FOUND, code);
    assert_eq!("a message longe", message);
}

#[test]
fn test_guard_catches_panic() {
    let (code, _) = run_guard(|| panic!("boom"));

    assert_eq!(abi::PANIC, code);
}

#[test]
fn test_abi_rejects_null_arguments() {
    // SAFETY: null pointers are allowed by every helper.
    unsafe {
        assert!(abi::cstr(core::ptr::null()).is_err());
        assert!(abi::read::<u64>(core::ptr::null()).is_err());
        assert!(abi::slice::<u64>(core::ptr::null(), 1).is_err());
        assert!(abi::slice::<u64>(core::ptr::null(), 0).is_ok());
        assert!(abi::write::<u64>(core::ptr::null_mut(), 1).is_err());
    }
}
