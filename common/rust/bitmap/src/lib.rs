//! Bitmap iterator.
//!
//! This module contains [`BitsIterator`] struct that can be used to iterate
//! over bits set in a 64-bit unsigned integer.

use core::ops::ControlFlow;

/// Iterator that allows to iterate over all bits set in the given 64-bit
/// unsigned integer.
///
/// Iteration is performed from the least significant bit to the most
/// significant one.
#[derive(Debug)]
pub struct BitsIterator {
    word: u64,
}

impl BitsIterator {
    /// Constructs a new [`BitsIterator`] from the given 64-bit unsigned
    /// integer.
    #[inline]
    pub const fn new(word: u64) -> Self {
        Self { word }
    }
}

impl Iterator for BitsIterator {
    type Item = usize;

    #[inline]
    fn next(&mut self) -> Option<Self::Item> {
        if self.word != 0 {
            let r = self.word.trailing_zeros();
            // This produces an integer with only the least significant bit of the word set,
            // which is equivalent to `1 << r`.
            //
            // But unlike bit shift, when combined with the following `xor` operator, it
            // compiles with a single `blsr` instruction.
            //
            // Which makes this function ~60% faster.
            let t = self.word & self.word.wrapping_neg();
            self.word ^= t;

            Some(r as usize)
        } else {
            None
        }
    }
}

/// Traverses all bits in the given word and calls the given function for each
/// bit.
///
/// Iteration is performed from the least significant bit to the most
/// significant one.
///
/// Returns [`ControlFlow::Break`] immediately if the prodived function returns
/// [`ControlFlow::Break`].
///
/// Otherwise returns [`ControlFlow::Continue`].
///
/// # Note
///
/// The [`ControlFlow`] type can be used with this function for the situations
/// in which you'd use break and continue in a normal loop.
///
/// If you never break, it is confirmed that the [`ControlFlow`] logic is
/// optimized out in optimized builds.
#[inline]
pub fn traverse_bits<F, B>(mut word: u64, mut f: F) -> ControlFlow<B>
where
    F: FnMut(usize) -> ControlFlow<B>,
{
    while word > 0 {
        let r = word.trailing_zeros() as usize;
        // This produces an integer with only the least significant bit of the word set,
        // which is equivalent to `1 << r`.
        //
        // But unlike bit shift, when combined with the following `xor` operator, it
        // compiles with a single blsr instruction.
        //
        // Which makes this function ~30% faster.
        let t = word & word.wrapping_neg();
        word ^= t;

        f(r)?;
    }

    ControlFlow::Continue(())
}

#[allow(dead_code)]
#[inline]
pub fn traverse_bits_rev<F, B>(mut word: u64, mut f: F) -> ControlFlow<B>
where
    F: FnMut(usize) -> ControlFlow<B>,
{
    while word > 0 {
        let r = 63 - word.leading_zeros() as usize;
        // Unfortunately, the `word & ~word` optimization is not possible here.
        let t = 1 << r;
        word ^= t;

        f(r)?;
    }

    ControlFlow::Continue(())
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn traverse_bits_check_each_bit() {
        let word = 0b1000001110111000;
        let expected = [3, 4, 5, 7, 8, 9, 15];

        let mut idx = 0;
        let c: ControlFlow<()> = traverse_bits(word, |bit| {
            assert_eq!(expected[idx], bit);
            idx += 1;
            ControlFlow::Continue(())
        });

        assert!(c.is_continue());
        assert_eq!(idx, expected.len());
    }

    #[test]
    fn traverse_bits_rev_check_each_bit() {
        let word = 0b1000001110111000;
        let expected = [15, 9, 8, 7, 5, 4, 3];

        let mut idx = 0;
        let c: ControlFlow<()> = traverse_bits_rev(word, |bit| {
            assert_eq!(expected[idx], bit);
            idx += 1;
            ControlFlow::Continue(())
        });

        assert!(c.is_continue());
        assert_eq!(idx, expected.len());
    }
}
