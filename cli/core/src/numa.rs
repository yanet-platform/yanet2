#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct NumaMap(u32);

impl NumaMap {
    pub const MAX: Self = Self(u32::MAX);

    #[inline]
    pub const fn new(numa: u32) -> Self {
        Self(numa)
    }

    #[inline]
    pub const fn as_u32(&self) -> u32 {
        match self {
            Self(v) => *v,
        }
    }
}

impl From<Vec<u32>> for NumaMap {
    fn from(indices: Vec<u32>) -> Self {
        let mut numa = 0;
        for idx in indices {
            numa |= 1 << idx;
        }

        Self(numa)
    }
}
