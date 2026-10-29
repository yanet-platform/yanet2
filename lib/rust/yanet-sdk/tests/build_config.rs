//! Direct initialization, rollback and release over the C-free memory owner.

use core::{cell::Cell, marker::PhantomData, panic::AssertUnwindSafe, sync::atomic::Ordering};
use std::panic::catch_unwind;

use yanet_sdk::{BuildError, Built, Module, Refusal, ShmSlice, boxed, build_config, slice, with_devices};
use yanet_sdk_testing::{
    View,
    cp::{Call, TestError, TestOwner},
};
use yanet_sys::{
    cp::{Owner, Stored},
    dp::ModuleConfig,
};

type Error = BuildError<TestError>;

/// Provides memory only for configurations without embedded C objects.
fn owner() -> TestOwner {
    // SAFETY: these tests build scalar values and owning edges only.
    unsafe { TestOwner::new() }
}

mod author {
    #![forbid(unsafe_code)]

    use core::sync::atomic::AtomicU64;

    use yanet_sdk::{CommitCx, Init, boxed, build_config, counter, device, prelude::*, slice};

    use super::{Built, Error, TestOwner};

    #[derive(ShmLayout)]
    #[repr(C)]
    pub struct Entry {
        pub ports: ShmSlice<u64>,
        pub device: Device,
        pub counter: Counter,
    }

    #[derive(ShmLayout)]
    #[repr(C)]
    pub struct Config {
        pub entries: ShmSlice<Entry>,
        pub default: ShmBox<Entry>,
        pub lanes: [ShmSlice<u64>; 2],
        pub mask: AtomicU64,
    }

    pub struct Data {
        entries: Vec<Vec<u64>>,
        default: Vec<u64>,
        lanes: [Vec<u64>; 2],
    }

    fn entry(ports: &[u64]) -> impl Init<Error, Output = Entry> {
        EntryInit {
            ports: slice(ports.len(), |index| Ok(ports[index])),
            device: device("eth0"),
            counter: counter("packets"),
        }
    }

    pub fn build(owner: &TestOwner, data: &Data) -> Result<Built<Policy, TestOwner>, Error> {
        build_config(
            owner,
            "policy",
            ConfigInit {
                entries: slice(data.entries.len(), |index| Ok(entry(&data.entries[index]))),
                default: boxed(entry(&data.default)),
                lanes: data
                    .lanes
                    .each_ref()
                    .map(|ports| slice(ports.len(), |index| Ok(ports[index]))),
                mask: AtomicU64::new(0xa5),
            },
        )
    }

    pub fn input() -> Data {
        Data {
            entries: vec![vec![80, 443], vec![]],
            default: vec![53],
            lanes: [vec![7, 8], vec![9]],
        }
    }

    pub struct Policy;
    impl Module for Policy {
        const NAME: &'static str = "policy";
        type Config = Config;
        type Prepared<'p> = ();
        fn prepare(_: &CommitCx<'_, Self>) {}
        fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
            batch.dispatch(|_| Verdict::Pass);
        }
    }
}

/// Borrows a complete frozen graph in its original owner's region.
fn body<'a, M: Module>(owner: &'a TestOwner, built: &'a Built<M, TestOwner>) -> (&'a M::Config, View<'a>) {
    // SAFETY: the original owner retains the complete frozen graph.
    unsafe {
        let config = &*owner.root().ptr::<ModuleConfig<M::Config>>(built.header().get());
        (config.body(), View::new(owner.root()))
    }
}

/// Releases a complete graph after its last reference has expired.
fn release<M: Module>(owner: &TestOwner, built: Built<M, TestOwner>) {
    // SAFETY: this owner built the graph and no references remain.
    unsafe { built.destroy(owner) };
}

#[test]
fn test_input_can_be_dropped_before_the_configuration_is_read() {
    let owner = owner();
    let built = author::build(&owner, &author::input()).unwrap();
    let (config, view) = body(&owner, &built);
    let entries = config.entries.as_slice(&view);
    assert_eq!([80, 443], entries[0].ports.as_slice(&view));
    assert!(entries[1].ports.as_slice(&view).is_empty());
    assert_eq!([53], config.default.get(&view).ports.as_slice(&view));
    assert_eq!([7, 8], config.lanes[0].as_slice(&view));
    assert_eq!([9], config.lanes[1].as_slice(&view));
    assert_eq!(0xa5, config.mask.load(Ordering::Relaxed));
    release(&owner, built);
    owner.assert_released();
}

#[test]
fn test_every_allocation_failure_releases_the_partial_graph() {
    let data = author::input();
    let mut succeeded = false;
    for limit in 0..20 {
        let owner = owner();
        owner.fail_after(Call::Alloc, limit);
        match author::build(&owner, &data) {
            Ok(built) => {
                release(&owner, built);
                succeeded = true;
            }
            Err(error) => assert!(matches!(error, BuildError::Refused(_))),
        }
        owner.assert_released();
        if succeeded {
            break;
        }
    }
    assert!(succeeded, "the sweep must reach a complete graph");
}

#[test]
fn test_every_registration_failure_releases_the_partial_graph() {
    let data = author::input();
    for limit in 0..=6 {
        let owner = owner();
        owner.fail_after(Call::Registration, limit);
        match author::build(&owner, &data) {
            Ok(built) => {
                assert_eq!(6, limit);
                release(&owner, built);
            }
            Err(error) => assert_eq!(BuildError::Owner(TestError::Registration), error),
        }
        owner.assert_released();
    }
}

#[test]
fn test_refused_header_releases_the_root() {
    let owner = owner();
    owner.fail_after(Call::Header, 0);
    let error = author::build(&owner, &author::input()).unwrap_err();
    assert_eq!(BuildError::Owner(TestError::Header), error);
    owner.assert_released();
}

/// Selects storage metadata without executing dataplane behavior.
struct Fixture<C>(PhantomData<fn() -> C>);
impl<C: Stored + Sync + 'static> Module for Fixture<C> {
    const NAME: &'static str = "fixture";
    type Config = C;
    type Prepared<'p> = ();
    fn prepare(_: &yanet_sdk::CommitCx<'_, Self>) {}
    fn handle<'id>(_: &mut yanet_sdk::Cx<'id, Self>, batch: &mut yanet_sdk::Batch<'id>) {
        batch.dispatch(|_| yanet_sdk::Verdict::Pass);
    }
}

#[test]
fn test_empty_slice_allocates_only_the_root_and_never_reads() {
    let owner = owner();
    let input = slice(0, |_| -> Result<u64, Error> { panic!("empty source was read") });
    let built = build_config::<Fixture<ShmSlice<u64>>, _, Error>(&owner, "words", input).unwrap();
    let (config, view) = body(&owner, &built);
    assert_eq!(0, config.offset());
    assert!(config.as_slice(&view).is_empty());
    assert_eq!(1, owner.live_blocks());
    release(&owner, built);
    owner.assert_released();
}

#[test]
fn test_slice_size_limits_are_refused_before_allocation_or_source_reads() {
    for len in [
        usize::MAX,
        yanet_sys::bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as usize / size_of::<u64>() + 1,
    ] {
        let owner = owner();
        owner.fail_after(Call::Alloc, 1);
        let input = slice(len, |_| -> Result<u64, Error> { panic!("refused source was read") });
        let error = build_config::<Fixture<ShmSlice<u64>>, _, Error>(&owner, "words", input).unwrap_err();
        assert_eq!(BuildError::Refused(Refusal::BlockTooLarge), error);
        owner.assert_released();
    }
}

mod rollback {
    #![forbid(unsafe_code)]
    use yanet_sdk::{Init, ShmBox, ShmLayout, ShmSlice, boxed, slice};

    use super::Error;

    #[derive(ShmLayout)]
    #[repr(C)]
    pub struct Entry {
        pub ports: ShmSlice<u64>,
        pub tail: ShmSlice<u64>,
    }

    #[derive(ShmLayout)]
    #[repr(C)]
    pub struct Config {
        pub boxed: ShmBox<Entry>,
        pub entries: ShmSlice<Entry>,
        pub pair: [Entry; 2],
        pub tail: Entry,
    }

    pub fn input(words: &[u64], position: usize) -> impl Init<Error, Output = Config> {
        let entry = move |index| EntryInit {
            ports: slice(words.len(), |index| Ok(words[index])),
            tail: slice(words.len(), move |offset| {
                assert!(index != position || offset != 1, "panic after a completed prefix");
                Ok(words[offset])
            }),
        };
        ConfigInit {
            boxed: boxed(entry(0)),
            entries: slice(3, move |index| Ok(entry(index + 1))),
            pair: [entry(4), entry(5)],
            tail: entry(6),
        }
    }
}

#[test]
fn test_source_panic_releases_nested_boxes_slices_arrays_and_fields() {
    for position in 0..7 {
        let owner = owner();
        let result = catch_unwind(AssertUnwindSafe(|| {
            build_config::<Fixture<rollback::Config>, _, Error>(
                &owner,
                "rollback",
                rollback::input(&[1, 2, 3], position),
            )
        }));
        assert!(result.is_err(), "panic position {position} was not reached");
        owner.assert_released();
    }
}

#[derive(Debug, PartialEq, Eq)]
enum DomainError {
    Build(Error),
    InvalidRecord(usize),
}
impl From<Error> for DomainError {
    fn from(error: Error) -> Self {
        Self::Build(error)
    }
}

#[test]
fn test_domain_error_after_nested_allocations_releases_the_completed_prefix() {
    let owner = owner();
    let input = slice(4, |index| {
        if index == 2 {
            return Err(DomainError::InvalidRecord(index));
        }
        Ok(boxed(slice(3, move |offset| Ok((index + offset) as u64))))
    });
    let error =
        build_config::<Fixture<ShmSlice<yanet_sdk::ShmBox<ShmSlice<u64>>>>, _, DomainError>(&owner, "domain", input)
            .unwrap_err();
    assert_eq!(DomainError::InvalidRecord(2), error);
    owner.assert_released();
}

#[test]
fn test_source_is_read_once_per_element_without_a_snapshot() {
    let owner = owner();
    let reads = Cell::new(0);
    let input = slice(3, |index| {
        assert_eq!(index, reads.get());
        reads.set(index + 1);
        Ok(index as u64)
    });
    let built = build_config::<Fixture<ShmSlice<u64>>, _, Error>(&owner, "words", input).unwrap();
    let (config, view) = body(&owner, &built);
    assert_eq!([0, 1, 2], config.as_slice(&view));
    assert_eq!(3, reads.get());
    release(&owner, built);
    owner.assert_released();
}

#[derive(yanet_sdk::ShmLayout)]
#[repr(C)]
struct Hygiene {
    __writer: u64,
    __destination: u64,
    __guard_0: yanet_sdk::ShmBox<u64>,
}

#[test]
fn test_derive_field_names_do_not_capture_generated_bindings() {
    let owner = owner();
    let built = build_config::<Fixture<Hygiene>, _, Error>(
        &owner,
        "hygiene",
        HygieneInit {
            __writer: 1u64,
            __destination: 2u64,
            __guard_0: boxed(3u64),
        },
    )
    .unwrap();
    let (config, view) = body(&owner, &built);
    assert_eq!(
        (1, 2, 3),
        (config.__writer, config.__destination, *config.__guard_0.get(&view))
    );
    release(&owner, built);
    owner.assert_released();
}

use yanet_packet as _;
use yanet_sdk_derive as _;
use yanet_shm as _;

#[test]
fn test_sdk_refusal_converts_to_the_domain_error() {
    let owner = owner();
    owner.fail_after(Call::Alloc, 0);
    let error = build_config::<Fixture<u64>, _, DomainError>(&owner, "domain", 7u64).unwrap_err();
    assert_eq!(DomainError::Build(Error::Refused(Refusal::OutOfMemory)), error);
    owner.assert_released();
}

#[test]
fn test_owner_error_converts_to_the_domain_error_without_losing_the_cause() {
    let owner = owner();
    owner.fail_after(Call::Header, 0);
    let error = build_config::<Fixture<u64>, _, DomainError>(&owner, "domain", 7u64).unwrap_err();
    assert_eq!(DomainError::Build(Error::Owner(TestError::Header)), error);
    owner.assert_released();
}

#[test]
fn test_callback_destructor_panic_releases_completed_nested_payloads() {
    struct Capture;
    impl Capture {
        fn value(&self, index: usize) -> u64 {
            index as u64
        }
    }
    impl Drop for Capture {
        fn drop(&mut self) {
            panic!("callback destructor");
        }
    }
    for len in [0, 3] {
        let owner = owner();
        let capture = Capture;
        let input = slice(len, move |index| Ok(boxed(capture.value(index))));
        let result = catch_unwind(AssertUnwindSafe(|| {
            build_config::<Fixture<ShmSlice<yanet_sdk::ShmBox<u64>>>, _, Error>(&owner, "drop", input)
        }));
        assert!(result.is_err());
        owner.assert_released();
    }
}

#[test]
fn test_bulk_devices_read_names_once_before_the_nested_initializer() {
    let owner = owner();
    let reads = Cell::new(0);
    let built = build_config::<Fixture<yanet_sdk::Device>, _, Error>(
        &owner,
        "devices",
        with_devices(
            3,
            |index| {
                assert_eq!(index, reads.get());
                reads.set(index + 1);
                Ok(["eth0", "eth1", "eth2"][index])
            },
            |devices| {
                assert_eq!(3, reads.get());
                assert!(devices.get(3).is_none());
                Ok(devices.get(2).unwrap())
            },
        ),
    )
    .unwrap();
    assert_eq!(2, body(&owner, &built).0.index());
    release(&owner, built);
    owner.assert_released();
}

#[test]
fn test_bulk_devices_partial_registration_errors_release_the_root() {
    for limit in 0..3 {
        let owner = owner();
        owner.fail_after(Call::Registration, limit);
        let input = with_devices(3, |index| Ok(["eth0", "eth1", "eth2"][index]), |_| Ok(7u64));
        let error = build_config::<Fixture<u64>, _, Error>(&owner, "devices", input).unwrap_err();
        assert_eq!(BuildError::Owner(TestError::Registration), error);
        owner.assert_released();
    }
}

#[test]
fn test_bulk_devices_name_and_constructor_domain_errors_release_the_root() {
    let owner = owner();
    let input = with_devices(
        3,
        |index| {
            if index == 2 {
                Err(DomainError::InvalidRecord(index))
            } else {
                Ok(["eth0", "eth1", "eth2"][index])
            }
        },
        |_| Ok(7u64),
    );
    let error = build_config::<Fixture<u64>, _, DomainError>(&owner, "devices", input).unwrap_err();
    assert_eq!(DomainError::InvalidRecord(2), error);
    owner.assert_released();

    let input = with_devices(
        3,
        |index| Ok(["eth0", "eth1", "eth2"][index]),
        |_| Err::<u64, _>(DomainError::InvalidRecord(3)),
    );
    let error = build_config::<Fixture<u64>, _, DomainError>(&owner, "devices", input).unwrap_err();
    assert_eq!(DomainError::InvalidRecord(3), error);
    owner.assert_released();
}

#[test]
fn test_bulk_devices_name_callback_drop_precedes_constructor_and_releases_the_root() {
    struct Capture;
    impl Capture {
        fn name(&self, index: usize) -> &'static str {
            ["eth0", "eth1", "eth2"][index]
        }
    }
    impl Drop for Capture {
        fn drop(&mut self) {
            panic!("device names callback destructor");
        }
    }
    for len in [0, 3] {
        let owner = owner();
        let capture = Capture;
        let made = Cell::new(false);
        let input = with_devices(
            len,
            move |index| Ok(capture.name(index)),
            |_| {
                made.set(true);
                Ok(7u64)
            },
        );
        let result = catch_unwind(AssertUnwindSafe(|| {
            build_config::<Fixture<u64>, _, Error>(&owner, "devices", input)
        }));
        assert!(result.is_err());
        assert!(!made.get());
        owner.assert_released();
    }
}

#[test]
fn test_bulk_devices_constructor_panic_releases_the_root() {
    let owner = owner();
    let input = with_devices(
        2,
        |index| Ok(["eth0", "eth1"][index]),
        |_| -> Result<u64, Error> {
            panic!("device constructor");
        },
    );
    let result = catch_unwind(AssertUnwindSafe(|| {
        build_config::<Fixture<u64>, _, Error>(&owner, "devices", input)
    }));
    assert!(result.is_err());
    owner.assert_released();
}

#[test]
fn test_counter_pair_owned_name_drop_panic_releases_header() {
    struct Name;
    impl AsRef<str> for Name {
        fn as_ref(&self) -> &str {
            "counted"
        }
    }
    impl Drop for Name {
        fn drop(&mut self) {
            panic!("counter name destructor");
        }
    }
    let owner = owner();
    let result = catch_unwind(AssertUnwindSafe(|| {
        build_config::<Fixture<yanet_sdk::CounterPair>, _, Error>(
            &owner,
            "counter",
            yanet_sdk::counter_pair("rules", Name),
        )
    }));
    assert!(result.is_err());
    owner.assert_released();
}
