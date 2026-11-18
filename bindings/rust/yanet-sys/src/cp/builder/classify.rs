//! The control-plane side of a classification: the compiles, the joins and
//! the decodes a build composes, each the C routine of `lib/classify`.
//!
//! A compile or a join makes a [`Branch`], a classifier with the stage of its
//! classes. A branch remembers the size of the ruleset it was compiled or
//! joined over, and a join or a decode refuses branches and projections of
//! other sizes: the identity of a ruleset is its length only, so the caller
//! keeps one ruleset per composition.

use core::{
    ffi::{CStr, c_int},
    fmt,
    marker::PhantomData,
    mem::{align_of, forget, size_of},
    ptr,
};

use yanet_shm::shm::MAX_ALIGN;

use super::{Brand, Build, BuildError, Kind, MemoryContext, Owned, Owner, Refusal, Scope, refused};
use crate::{
    bindings,
    classify::{Attribute, Decode, Join, Node, Rules},
};

/// Owns the storage of a C output, but none of the allocations it may name.
struct NativeSlot<'b, T, W: Owner> {
    raw: *mut T,
    memory: &'b MemoryContext<'b, W>,
    _brand: Brand<'b>,
}

impl<'b, T, W: Owner> NativeSlot<'b, T, W> {
    fn reserve<E: From<BuildError<W::Error>>, K: Kind>(scope: Scope<'b, '_, W, E, K>) -> Result<Self, E> {
        const { assert!(size_of::<T>() > 0 && align_of::<T>() <= MAX_ALIGN) };
        let addr = scope.alloc(size_of::<T>())?;
        // Own the address before asking the owner for its provenance.
        let mut slot = Self {
            raw: ptr::without_provenance_mut(addr),
            memory: scope.memory,
            _brand: PhantomData,
        };
        slot.raw = slot.memory.root().ptr(addr);
        Ok(slot)
    }

    fn zero(&self) -> *mut T {
        // SAFETY: the exclusive slot is aligned for one whole value.
        unsafe { self.raw.write_bytes(0, 1) };
        self.raw
    }

    fn context(&self) -> *mut bindings::memory_context {
        self.raw.with_addr(self.memory.live.context().get()).cast()
    }

    /// # Safety
    ///
    /// The writer must have written a whole value at its final address,
    /// with all owned edges belonging to this build.
    unsafe fn finish(self) -> Owned<'b, T, W>
    where
        T: Build,
    {
        let value = Owned {
            addr: self.raw.addr(),
            memory: self.memory,
            _brand: PhantomData,
            _value: PhantomData,
        };
        forget(self);
        value
    }
}

impl<T, W: Owner> Drop for NativeSlot<'_, T, W> {
    fn drop(&mut self) {
        // SAFETY: the reservation still owns this unreferenced block of the
        // original size. The caller handles any allocations named within.
        unsafe { self.memory.free(self.raw.addr(), size_of::<T>()) };
    }
}

/// The classes of a stage of a composition over a ruleset of build `'b`: the
/// registry of its class space and the group of each rule.
///
/// Held by a [`Branch`] and read by the joins and the decodes of the same
/// build and ruleset; it is scratch of the build in its memory, which it
/// returns when dropped.
struct Stage<'b, W: Owner> {
    slot: NativeSlot<'b, bindings::classifier, W>,
    rules: u32,
}

impl<W: Owner> fmt::Debug for Stage<'_, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("Stage")
            .field("addr", &self.raw().addr())
            .field("rules", &self.rules)
            .finish()
    }
}

impl<'b, W: Owner> Stage<'b, W> {
    fn zero(slot: NativeSlot<'b, bindings::classifier, W>, rules: u32) -> Self {
        slot.zero();
        Self { slot, rules }
    }

    fn raw(&self) -> *mut bindings::classifier {
        self.slot.raw
    }
}

impl<W: Owner> Drop for Stage<'_, W> {
    fn drop(&mut self) {
        // SAFETY: a zeroed stage is finalizable even after partial output.
        // The slot and its allocations still belong to this live context.
        unsafe { bindings::yanet_sys_classify_stage_free(self.raw(), self.slot.context(), self.rules) };
        // The reservation returns its storage only after finalization.
    }
}

/// The rule count of a ruleset as C takes it.
fn rule_count(len: usize) -> Result<u32, Refusal> {
    u32::try_from(len).map_err(|_| Refusal::TooManyItems)
}

/// Refuses inputs of a join or a decode over rulesets of different sizes.
fn same_ruleset(sizes: &[usize]) -> Result<(), Refusal> {
    match sizes {
        [first, rest @ ..] if rest.iter().any(|size| size != first) => Err(Refusal::RuleSetMismatch),
        _ => Ok(()),
    }
}

/// A classifier of a tree a build wrote, with the stage of its classes.
///
/// Made by [`Scope::compile`] and [`Scope::join`] only, and taken whole by the
/// joins and the decode, so a node always reads the classes of the classifiers
/// it holds. Dropped, it returns the classifier and the stage.
///
/// ```compile_fail,E0451
/// use yanet_sys::{
///     classify::Net4Attr,
///     cp::{Branch, Owned, Owner},
/// };
/// fn forge<'b, W: Owner>(value: Owned<'b, Net4Attr, W>) -> Branch<'b, Net4Attr, W> {
///     Branch { value, stage: unimplemented!() }
/// }
/// ```
#[must_use]
pub struct Branch<'b, T: Build, W: Owner> {
    value: Owned<'b, T, W>,
    stage: Stage<'b, W>,
}

impl<T: Build, W: Owner> fmt::Debug for Branch<'_, T, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("Branch").field("stage", &self.stage).finish()
    }
}

impl<'b, W: Owner, E: From<BuildError<W::Error>>, K: Kind> Scope<'b, '_, W, E, K> {
    /// Joins two branches of the same ruleset into a branch that holds them.
    ///
    /// # Errors
    ///
    /// The branches are over rulesets of different sizes, or the memory is
    /// exhausted; nothing of the join or the branches is left allocated.
    pub fn join<L: Node, R: Node>(
        self,
        name: &CStr,
        left: Branch<'b, L, W>,
        right: Branch<'b, R, W>,
    ) -> Result<Branch<'b, Join<L, R>, W>, E> {
        if !W::C_MEMORY {
            return Err(refused::<W, E>(Refusal::NoCMemory));
        }
        same_ruleset(&[left.stage.rules as usize, right.stage.rules as usize]).map_err(refused::<W, E>)?;
        let (Branch { value: left, stage: left_stage }, Branch { value: right, stage: right_stage }) = (left, right);
        self.staged(left_stage.rules, |context, stage, join: *mut Join<L, R>| {
            // SAFETY: both stages were filled over the rule count, and the
            // joint leads the zeroed node.
            let status = unsafe {
                bindings::yanet_sys_classify_join(
                    context,
                    name.as_ptr(),
                    left_stage.raw(),
                    right_stage.raw(),
                    left_stage.rules,
                    join.cast(),
                    stage,
                )
            };
            if status == 0 {
                // SAFETY: the prefix was joined from these inputs' stages
                // and the exclusive node is in their build.
                unsafe { Join::write_inputs(join, left, right) };
            }
            status
        })
    }

    /// Joins two branches into the first rule of a projection of their
    /// ruleset each pair of their classes matches, holding the branches.
    ///
    /// A rule is in the projection when its slot is `Some`, whatever it
    /// holds.
    ///
    /// # Errors
    ///
    /// The branches and the projection are over rulesets of different sizes,
    /// or the memory is exhausted; nothing of the join or the branches is
    /// left allocated.
    pub fn join_rules<L: Node, R: Node, P>(
        self,
        name: &CStr,
        left: Branch<'b, L, W>,
        right: Branch<'b, R, W>,
        rules: &[Option<&P>],
    ) -> Result<Owned<'b, Rules<L, R>, W>, E> {
        if !W::C_MEMORY {
            return Err(refused::<W, E>(Refusal::NoCMemory));
        }
        same_ruleset(&[left.stage.rules as usize, right.stage.rules as usize, rules.len()]).map_err(refused::<W, E>)?;
        let (Branch { value: left, stage: left_stage }, Branch { value: right, stage: right_stage }) = (left, right);
        self.written(|context, table: *mut Rules<L, R>| {
            // SAFETY: both stages were filled over the rule count of the
            // projection, whose slots C only tests for null; the table leads
            // the zeroed node.
            let status = unsafe {
                bindings::yanet_sys_classify_join_rules(
                    context,
                    name.as_ptr(),
                    left_stage.raw(),
                    right_stage.raw(),
                    rules.as_ptr().cast(),
                    left_stage.rules,
                    table.cast(),
                )
            };
            if status == 0 {
                // SAFETY: the prefix was joined from these inputs' stages
                // and the exclusive node is in their build.
                unsafe { Rules::write_inputs(table, left, right) };
            }
            status
        })
    }

    /// Decodes a branch into the first rule of a projection of its ruleset
    /// each of its classes matches, holding the branch.
    ///
    /// A rule is in the projection when its slot is `Some`, whatever it
    /// holds.
    ///
    /// # Errors
    ///
    /// The branch and the projection are over rulesets of different sizes, or
    /// the memory is exhausted; nothing of the decode or the branch is left
    /// allocated.
    pub fn decode<N: Node, P>(
        self,
        name: &CStr,
        input: Branch<'b, N, W>,
        rules: &[Option<&P>],
    ) -> Result<Owned<'b, Decode<N>, W>, E> {
        if !W::C_MEMORY {
            return Err(refused::<W, E>(Refusal::NoCMemory));
        }
        same_ruleset(&[input.stage.rules as usize, rules.len()]).map_err(refused::<W, E>)?;
        let Branch { value: input, stage } = input;
        self.written(|context, map: *mut Decode<N>| {
            // SAFETY: the stage was filled over the rule count of the
            // projection, whose slots C only tests for null; the map leads
            // the zeroed node.
            let status = unsafe {
                bindings::yanet_sys_classify_decode(
                    context,
                    name.as_ptr(),
                    stage.raw(),
                    rules.as_ptr().cast(),
                    stage.rules,
                    map.cast(),
                )
            };
            if status == 0 {
                // SAFETY: the prefix was decoded from this input's stage
                // and the exclusive node is in its build.
                unsafe { Decode::write_input(map, input) };
            }
            status
        })
    }

    /// Compiles the classifier of an attribute over a ruleset, one view of
    /// the attribute per rule, into a branch.
    ///
    /// The name titles the memory contexts of the compile in the memory tree.
    ///
    /// # Errors
    ///
    /// The rules are more than C counts, or the memory is exhausted; nothing
    /// of the compile is left allocated.
    pub fn compile<A: Attribute>(self, name: &CStr, rules: &[Option<&A>]) -> Result<Branch<'b, A::Classifier, W>, E> {
        if !W::C_MEMORY {
            return Err(refused::<W, E>(Refusal::NoCMemory));
        }
        let count = rule_count(rules.len()).map_err(refused::<W, E>)?;
        self.staged(count, |memory, stage, classifier| {
            // SAFETY: a rule slot is a nullable pointer to a view the
            // attribute's compile reads as its C view, borrowed for the call.
            unsafe { A::compile(memory, name.as_ptr(), rules.as_ptr().cast(), count, classifier, stage) }
        })
    }

    /// Allocates the block of a `T` for a C routine of the build's memory
    /// context that writes it whole, returning zero, or leaves it with
    /// nothing allocated.
    ///
    /// The block is zeroed first, as C callers zero what they hand a compile:
    /// a routine that fails part way frees every field of its output, those
    /// it never reached included, which only a zeroed field makes a no-op.
    fn written<T: Build>(
        self,
        write: impl FnOnce(*mut bindings::memory_context, *mut T) -> c_int,
    ) -> Result<Owned<'b, T, W>, E> {
        let slot = NativeSlot::reserve(self)?;
        let value = slot.zero();
        if write(slot.context(), value) != 0 {
            drop(slot);
            // Memory is C's only failure for the routines of this module.
            return Err(refused::<W, E>(Refusal::OutOfMemory));
        }
        // SAFETY: success wrote the whole value, including any input edges,
        // at the final address of the reservation.
        Ok(unsafe { slot.finish() })
    }

    /// As [`Scope::written`], for a routine that also fills a stage over the
    /// rule count.
    ///
    /// The stage is zeroed first and finalized when the routine fails: a
    /// failed compile may leave part of it allocated, which its C callers
    /// release the same way.
    fn staged<T: Build>(
        self,
        rules: u32,
        write: impl FnOnce(*mut bindings::memory_context, *mut bindings::classifier, *mut T) -> c_int,
    ) -> Result<Branch<'b, T, W>, E> {
        let stage = Stage::zero(NativeSlot::reserve(self)?, rules);
        let value = self.written(|context, value| write(context, stage.raw(), value))?;
        Ok(Branch { value, stage })
    }
}

#[cfg(test)]
mod tests {
    use super::{Refusal, rule_count, same_ruleset};

    /// verifies that inputs over one ruleset size pass and any other size
    /// among them is refused, whichever input differs.
    #[test]
    fn test_same_ruleset_refuses_any_other_size() {
        for (sizes, result) in [
            (&[3, 3][..], Ok(())),
            (&[3, 3, 3], Ok(())),
            (&[0, 0], Ok(())),
            (&[3, 2], Err(Refusal::RuleSetMismatch)),
            (&[3, 3, 4], Err(Refusal::RuleSetMismatch)),
            (&[4, 3, 3], Err(Refusal::RuleSetMismatch)),
        ] {
            assert_eq!(result, same_ruleset(sizes), "{sizes:?}");
        }
    }

    /// verifies that a ruleset of up to `u32::MAX` rules is counted and a
    /// larger one refused.
    #[test]
    fn test_rule_count_refuses_more_rules_than_c_counts() {
        assert_eq!(Ok(u32::MAX), rule_count(u32::MAX as usize));
        assert_eq!(Err(Refusal::TooManyItems), rule_count(u32::MAX as usize + 1));
    }
}
