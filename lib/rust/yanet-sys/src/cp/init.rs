//! Safe initializers consumed directly at their final shared-memory address.

use yanet_shm::rel::{ShmBox, ShmSlice};

use super::{BuildError, Encode, Owner, Refusal, Writer};
use crate::{
    common::lpm::{Lpm, LpmRange},
    dp::{Counter, Device},
};

/// Streams fallible records without retaining an intermediate collection.
pub fn slice<E, I, F>(len: usize, fill: F) -> Slice<F>
where
    F: FnMut(usize) -> Result<I, E>,
{
    Slice { len, fill }
}

/// A sized sequence whose callback is consumed once per element.
pub struct Slice<F> {
    len: usize,
    fill: F,
}

// SAFETY: the writer guards the allocation and every completed element.
unsafe impl<E, I, F> Encode<E> for Slice<F>
where
    I: Encode<E>,
    F: FnMut(usize) -> Result<I, E>,
{
    type Output = ShmSlice<I::Output>;

    unsafe fn encode<W>(self, writer: &mut Writer<'_, W>, destination: *mut Self::Output) -> Result<(), E>
    where
        W: Owner,
        E: From<BuildError<W::Error>>,
    {
        // SAFETY: the caller supplies the exclusive final slice field.
        unsafe { writer.slice(self.len, self.fill, destination) }
    }
}

/// Places a recursively initialized value in its own payload block.
pub fn boxed<I>(input: I) -> Boxed<I> {
    Boxed(input)
}

/// A separately allocated value, owned by its final shared-memory edge.
pub struct Boxed<I>(I);

// SAFETY: the writer owns the payload until its complete edge is stored.
unsafe impl<E, I: Encode<E>> Encode<E> for Boxed<I> {
    type Output = ShmBox<I::Output>;

    unsafe fn encode<W>(self, writer: &mut Writer<'_, W>, destination: *mut Self::Output) -> Result<(), E>
    where
        W: Owner,
        E: From<BuildError<W::Error>>,
    {
        // SAFETY: the caller supplies the exclusive final box field.
        unsafe { writer.boxed(self.0, destination) }
    }
}

/// Registers a single-value counter while writing its final field.
pub fn counter(name: &str) -> CounterName<'_> {
    CounterName(name)
}

/// A counter name borrowed until construction returns.
pub struct CounterName<'a>(&'a str);

// SAFETY: registration supplies the matching index before it is stored.
unsafe impl<E> Encode<E> for CounterName<'_> {
    type Output = Counter;

    unsafe fn encode<W>(self, writer: &mut Writer<'_, W>, destination: *mut Counter) -> Result<(), E>
    where
        W: Owner,
        E: From<BuildError<W::Error>>,
    {
        // SAFETY: the caller supplies the exclusive final counter field.
        unsafe { writer.counter(self.0, destination) }.map_err(E::from)
    }
}

/// Links a device by name while writing its final field.
pub fn device(name: &str) -> DeviceName<'_> {
    DeviceName(name)
}

/// A device name borrowed until construction returns.
pub struct DeviceName<'a>(&'a str);

// SAFETY: linking supplies the matching index before it is stored.
unsafe impl<E> Encode<E> for DeviceName<'_> {
    type Output = Device;

    unsafe fn encode<W>(self, writer: &mut Writer<'_, W>, destination: *mut Device) -> Result<(), E>
    where
        W: Owner,
        E: From<BuildError<W::Error>>,
    {
        // SAFETY: the caller supplies the exclusive final device field.
        unsafe { writer.device(self.0, destination) }.map_err(E::from)
    }
}

/// Declares a top-level device table once before constructing its records.
///
/// Each name is read and linked once. Declarations must be unique and append
/// contiguously to the configuration's device namespace; its prelinked empty
/// default name may appear once anywhere. The callback receives an allocation-
/// free mapping from declaration positions to namespace-relative indices.
pub fn with_devices<'src, E, I, F, G>(len: usize, read_names: F, make: G) -> WithDevices<F, G>
where
    F: FnMut(usize) -> Result<&'src str, E>,
    G: FnOnce(DeviceMap) -> Result<I, E>,
    I: Encode<E>,
{
    WithDevices { len, read_names, make }
}

/// A device declaration and the initializer that consumes its mapping.
pub struct WithDevices<F, G> {
    len: usize,
    read_names: F,
    make: G,
}

/// A constant-size map from declaration positions to device indices.
///
/// Indices are relative to a configuration's device namespace. They neither
/// own a control-plane object nor prove registration in another configuration;
/// reusing them there can select a different device or fail resolution.
#[derive(Clone, Copy, Debug)]
pub struct DeviceMap {
    base: u64,
    len: usize,
    default_hole: Option<usize>,
}

impl DeviceMap {
    /// Returns the declared device, or none beyond the declaration table.
    pub fn get(self, index: usize) -> Option<DeviceIndex> {
        if index >= self.len {
            return None;
        }
        if self.default_hole == Some(index) {
            return Some(DeviceIndex(0));
        }
        let offset = index - usize::from(self.default_hole.is_some_and(|hole| index > hole));
        self.base.checked_add(u64::try_from(offset).ok()?).map(DeviceIndex)
    }

    fn push(&mut self, id: u64, default: bool) -> Result<(), Refusal> {
        if self.len == 0 {
            self.base = id;
        }
        let offset = self.len - usize::from(self.default_hole.is_some());
        let expected = self
            .base
            .checked_add(u64::try_from(offset).map_err(|_| Refusal::InvalidDeviceTable)?)
            .ok_or(Refusal::InvalidDeviceTable)?;
        if id != expected {
            if id == 0 && default && self.base > 0 && self.default_hole.is_none() {
                self.default_hole = Some(self.len);
            } else {
                return Err(Refusal::InvalidDeviceTable);
            }
        }
        self.len = self.len.checked_add(1).ok_or(Refusal::InvalidDeviceTable)?;
        Ok(())
    }
}

/// A namespace-relative device index obtained from a declaration table.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct DeviceIndex(u64);

// SAFETY: device indices are scalar words; resolution checks namespace bounds.
unsafe impl<E> Encode<E> for DeviceIndex {
    type Output = Device;

    unsafe fn encode<W>(self, _: &mut Writer<'_, W>, destination: *mut Device) -> Result<(), E>
    where
        W: Owner,
        E: From<BuildError<W::Error>>,
    {
        // SAFETY: the caller supplies the exclusive final scalar device field.
        unsafe { destination.cast::<u64>().write(self.0) };
        Ok(())
    }
}

// SAFETY: declarations own no payload; the nested encoder guards its fields.
unsafe impl<'src, E, I, F, G> Encode<E> for WithDevices<F, G>
where
    F: FnMut(usize) -> Result<&'src str, E>,
    G: FnOnce(DeviceMap) -> Result<I, E>,
    I: Encode<E>,
{
    type Output = I::Output;

    unsafe fn encode<W>(self, writer: &mut Writer<'_, W>, destination: *mut Self::Output) -> Result<(), E>
    where
        W: Owner,
        E: From<BuildError<W::Error>>,
    {
        let Self { len, mut read_names, make } = self;
        let mut devices = DeviceMap { base: 0, len: 0, default_hole: None };
        for index in 0..len {
            let name = read_names(index)?;
            let id = writer.link_device(name)?;
            devices.push(id, name.is_empty()).map_err(BuildError::Refused)?;
        }
        drop(read_names);
        let input = make(devices)?;
        // SAFETY: the caller's exclusive destination has the nested output
        // type.
        unsafe { input.encode(writer, destination) }
    }
}

/// Streams inclusive ranges into a C table; later overlapping ranges win.
///
/// Each fallible callback is read once and its returned endpoints are checked
/// immediately before insertion. Errors and panics release the partial table.
pub fn lpm<E, const K: usize, F>(len: usize, fill: F) -> LpmInit<K, F>
where
    F: FnMut(usize) -> Result<LpmRange<K>, E>,
{
    LpmInit { len, fill }
}

/// A table initializer borrowing only the data captured by its callback.
pub struct LpmInit<const K: usize, F> {
    len: usize,
    fill: F,
}

// SAFETY: the writer guards the C context across reads, errors and panics.
unsafe impl<E, const K: usize, F> Encode<E> for LpmInit<K, F>
where
    F: FnMut(usize) -> Result<LpmRange<K>, E>,
{
    type Output = Lpm<K>;

    unsafe fn encode<W>(self, writer: &mut Writer<'_, W>, destination: *mut Self::Output) -> Result<(), E>
    where
        W: Owner,
        E: From<BuildError<W::Error>>,
    {
        // SAFETY: the caller supplies the exclusive final table field.
        unsafe { writer.lpm(self.len, self.fill, destination) }
    }
}

#[cfg(test)]
mod tests {
    use super::{DeviceIndex, DeviceMap, Refusal};

    #[test]
    fn test_device_map_observed_ids_preserve_the_default_at_every_position() {
        for ids in [
            &[1, 2, 0, 3][..],
            &[0, 1, 2][..],
            &[1, 2, 0][..],
            &[1, 2, 3][..],
            &[][..],
        ] {
            let mut map = DeviceMap { base: 0, len: 0, default_hole: None };
            for &id in ids {
                map.push(id, id == 0).unwrap();
            }
            for (index, &id) in ids.iter().enumerate() {
                assert_eq!(Some(DeviceIndex(id)), map.get(index));
            }
            assert_eq!(None, map.get(ids.len()));
            assert_eq!(None, map.get(usize::MAX));
        }
    }

    #[test]
    fn test_device_map_rejects_duplicate_and_noncontiguous_observed_ids() {
        for ids in [&[1, 1][..], &[0, 0][..], &[1, 0, 0][..], &[1, 3][..]] {
            let mut map = DeviceMap { base: 0, len: 0, default_hole: None };
            for &id in &ids[..ids.len() - 1] {
                map.push(id, id == 0).unwrap();
            }
            assert_eq!(Err(Refusal::InvalidDeviceTable), map.push(ids[ids.len() - 1], true));
        }
    }

    #[test]
    fn test_device_map_rejects_index_overflow_without_wrapping() {
        let mut map = DeviceMap { base: 0, len: 0, default_hole: None };
        map.push(u64::MAX, false).unwrap();
        assert_eq!(Some(DeviceIndex(u64::MAX)), map.get(0));
        assert_eq!(Err(Refusal::InvalidDeviceTable), map.push(0, false));
        assert_eq!(None, map.get(1));
    }
}
