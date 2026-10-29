//! Safe initializers consumed directly at their final shared-memory address.

use yanet_shm::rel::{ShmBox, ShmSlice};

use super::{BuildError, Encode, Owner, Writer};
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
