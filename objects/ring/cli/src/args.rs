use clap::Parser;
use clap_complete::engine::ArgValueCandidates;

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Create a named ring and publish it.
    Create(CreateCmd),
    /// List registered ring objects.
    List,
    /// Show the capacity and publish batch of a named ring.
    Show(ShowCmd),
    /// Delete a named ring.
    Delete(DeleteCmd),
}

impl ModeCmd {
    pub(crate) fn action(&self) -> &'static str {
        match self {
            Self::Create(..) => "create",
            Self::List => "list",
            Self::Show(..) => "show",
            Self::Delete(..) => "delete",
        }
    }
}

/// Parses a per-worker capacity such as 1MiB or 4096 into bytes.
///
/// The value must be a power of two. So decimal units such as 1MB are
/// rejected. The service checks the range.
pub(crate) fn parse_capacity(raw: &str) -> Result<u64, String> {
    let bytes = raw
        .parse::<bytesize::ByteSize>()
        .map(|size| size.as_u64())
        .map_err(|_| format!("expected a size such as 1MiB, got {raw:?}"))?;
    if !bytes.is_power_of_two() {
        return Err(format!(
            "{raw:?} is {bytes} bytes, not a power of two; use IEC units such as 64KiB or 1MiB (1MB is 1000000 bytes)"
        ));
    }
    Ok(bytes)
}

#[derive(Debug, Clone, Parser)]
pub struct CreateCmd {
    /// Name of the ring to create.
    #[arg(long = "name", short = 'n')]
    pub name: String,

    /// Per-worker buffer size. A power of two in bytes or in IEC units, such
    /// as 64KiB or 1MiB.
    #[arg(long, value_parser = parse_capacity)]
    pub capacity: u64,

    /// Records a writer commits before it publishes them by itself. From 1 to
    /// 1024. If omitted, the service uses its default.
    #[arg(long, value_parser = clap::value_parser!(u32).range(1..=1024))]
    pub publish_batch: Option<u32>,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// Name of the ring to show.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::ring_candidates))]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// Name of the ring to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::ring_candidates))]
    pub name: String,
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn test_parse_capacity_accepts_powers_of_two() {
        for (raw, bytes) in [("64", 64), ("4KiB", 4096), ("1MiB", 1 << 20)] {
            assert_eq!(Ok(bytes), parse_capacity(raw), "{raw}");
        }
    }

    #[test]
    fn test_parse_capacity_rejects_bad_values() {
        for raw in ["lots", "1MB", "24", "0"] {
            assert!(parse_capacity(raw).is_err(), "{raw}");
        }
    }
}
