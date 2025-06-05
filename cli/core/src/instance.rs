#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct InstanceMap(u32);

impl InstanceMap {
    pub const MAX: Self = Self(u32::MAX);

    #[inline]
    pub const fn new(instance_map: u32) -> Self {
        Self(instance_map)
    }

    #[inline]
    pub const fn as_u32(&self) -> u32 {
        match self {
            Self(v) => *v,
        }
    }
}

impl From<Vec<u32>> for InstanceMap {
    fn from(indices: Vec<u32>) -> Self {
        let mut map = 0;
        for idx in indices {
            map |= 1 << idx;
        }

        Self(map)
    }
}
