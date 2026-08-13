use clap::Parser;
use clap_complete::engine::ArgValueCandidates;

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List registered fwstate-map objects
    List,
    /// Create a named fwstate-map and publish it
    Create(CreateCmd),
    /// Delete a named fwstate-map
    Delete(DeleteCmd),
    /// Show statistics for a named fwstate-map
    Stats(StatsCmd),
    /// List entries from a named fwstate-map
    Entries(EntriesCmd),
    /// Insert a new layer into a named fwstate-map's table chain
    InsertLayer(InsertLayerCmd),
}

/// Address family of a fwstate-map object.
#[derive(Debug, Clone, Copy, PartialEq, Eq, clap::ValueEnum)]
pub enum MapKind {
    /// IPv4 state table
    V4,
    /// IPv6 state table
    V6,
}

#[derive(Debug, Clone, Parser)]
pub struct CreateCmd {
    /// Name of the fwstate-map to create
    #[arg(long = "name", short = 'n')]
    pub map_name: String,

    /// Address family of the state table this map owns
    #[arg(long)]
    pub kind: MapKind,

    /// Size of the hash table index (0 uses the service default)
    #[arg(long)]
    pub index_size: Option<u32>,

    /// Number of extra collision buckets (0 uses the service default)
    #[arg(long)]
    pub extra_bucket_count: Option<u32>,

    /// Per-worker state sizing (0 derives the dataplane worker count)
    #[arg(long)]
    pub worker_count: Option<u32>,
}

/// Rotates a live map: the new layer becomes the active head and expired
/// tails are reclaimed after a generation barrier.
#[derive(Debug, Clone, Parser)]
pub struct InsertLayerCmd {
    /// Name of the fwstate-map to rotate
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::map_candidates))]
    pub map_name: String,

    /// Size of the hash table index (0 uses the service default)
    #[arg(long)]
    pub index_size: Option<u32>,

    /// Number of extra collision buckets (0 uses the service default)
    #[arg(long)]
    pub extra_bucket_count: Option<u32>,

    /// Per-worker state sizing (0 derives the dataplane worker count)
    #[arg(long)]
    pub worker_count: Option<u32>,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// Name of the fwstate-map to delete
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::map_candidates))]
    pub map_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ListCmd;

#[derive(Debug, Clone, Parser)]
pub struct StatsCmd {
    /// Name of the fwstate-map to show statistics for
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::map_candidates))]
    pub map_name: String,
}

#[derive(Debug, Clone, clap::ValueEnum)]
pub enum DirectionArg {
    Forward,
    Backward,
}

/// Arguments of `entries`: one fwstate-map owns a single family's table,
/// so no family selection is needed.
#[derive(Debug, Clone, Parser)]
pub struct EntriesCmd {
    /// Name of the fwstate-map to iterate
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::map_candidates))]
    pub map_name: String,

    /// Layer index to iterate (0 = active layer)
    #[arg(long, default_value = "0")]
    pub layer: u32,

    /// Include expired entries
    #[arg(long)]
    pub include_expired: bool,

    /// Max entries per gRPC batch
    #[arg(long, default_value = "128")]
    pub batch: u32,

    /// Total number of entries to return (0 = unlimited)
    #[arg(long, default_value = "0")]
    pub count: u32,

    /// Iteration direction
    #[arg(long, default_value = "forward")]
    pub direction: DirectionArg,

    /// Starting cursor position (0 = beginning)
    #[arg(long, default_value = "0")]
    pub index: u32,
}
