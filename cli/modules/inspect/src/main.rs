//! CLI for YANET "inspect" module.

use core::cmp::Reverse;
use std::collections::BTreeMap;

use bytesize::ByteSize;
use clap::{ArgAction, Parser};
use ptree::TreeBuilder;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{inspect_service_client::InspectServiceClient, InspectRequest, InspectResponse, MemoryNode};

const INSPECT_SERVICE: &str = "controlplane.ynpb.v1.InspectService";

/// Inspect module - displays system introspection information.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

pub fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = InspectService::new(&cmd.connection).await?;
    let response = service.inspect().await?;

    output::data(|| &response, || render_tree(&response));

    Ok(())
}

pub struct InspectService {
    service: Service<InspectServiceClient<LayeredChannel>>,
}

impl InspectService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect_for(connection, "inspect", INSPECT_SERVICE, |channel| {
            InspectServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn inspect(&mut self) -> Result<InspectResponse, Error> {
        let response = self
            .service
            .client()
            .inspect(InspectRequest {})
            .await
            .map_err(self.service.status("inspect"))?
            .into_inner();

        Ok(response)
    }
}

fn render_tree(response: &InspectResponse) {
    let Some(info) = &response.instance_info else {
        output::empty(format_args!("No instance information found."));
        return;
    };

    let mut tree = TreeBuilder::new("YANET System".to_string());

    tree.begin_child(format!("Instance {}", info.instance_idx));

    tree.begin_child(format!("Attached to NUMA {}", info.numa_idx));
    tree.end_child();

    tree.begin_child("Dataplane Modules".to_string());
    if info.dp_modules.is_empty() {
        tree.add_empty_child("(none)".to_owned());
    }

    for (idx, module) in info.dp_modules.iter().enumerate() {
        tree.add_empty_child(format!("{}: {}", idx, module.name));
    }
    tree.end_child();

    tree.begin_child("Controlplane Configurations".to_string());
    if info.cp_configs.is_empty() {
        tree.add_empty_child("(none)".to_owned());
    }

    for cfg in &info.cp_configs {
        tree.add_empty_child(format!("{}:{} (gen: {})", cfg.r#type, cfg.name, cfg.generation));
    }
    tree.end_child();

    tree.begin_child("Agents".to_string());
    if info.agents.is_empty() {
        tree.add_empty_child("(none)".to_owned());
    }

    for agent in &info.agents {
        tree.begin_child(agent.name.to_string());
        if agent.instances.is_empty() {
            tree.add_empty_child("(none)".to_owned());
        }

        for instance in &agent.instances {
            let used = instance.memory_limit.saturating_sub(instance.free_bytes);
            tree.begin_child(format!("Instance (PID: {})", instance.pid));
            tree.add_empty_child(format!("Memory limit: {}", ByteSize::b(instance.memory_limit)));
            tree.add_empty_child(format!("Used:         {}", ByteSize::b(used)));
            tree.add_empty_child(format!("Free:         {}", ByteSize::b(instance.free_bytes)));
            tree.add_empty_child(format!("Generation: {}", instance.generation));
            if !instance.memory_tree.is_empty() {
                add_memory_tree(&mut tree, &instance.memory_tree);
            }
            tree.end_child();
        }

        tree.end_child();
    }
    tree.end_child();

    tree.begin_child("Functions".to_string());
    if info.functions.is_empty() {
        tree.add_empty_child("(none)".to_owned());
    }

    for function in &info.functions {
        tree.begin_child(format!("Function {}", function.name));
        if function.chains.is_empty() {
            tree.add_empty_child("(none)".to_owned());
        }

        for chain in &function.chains {
            tree.begin_child(format!("Chain {} (weight {})", chain.name, chain.weight));
            if chain.modules.is_empty() {
                tree.add_empty_child("(none)".to_owned());
            }

            for module in &chain.modules {
                tree.add_empty_child(format!("Module {}:{}", module.r#type, module.name));
            }
            tree.end_child();
        }
        tree.end_child();
    }
    tree.end_child();

    tree.begin_child("Pipelines".to_string());
    if info.pipelines.is_empty() {
        tree.add_empty_child("(none)".to_owned());
    }

    for pipeline in &info.pipelines {
        tree.begin_child(format!("Pipeline {}", pipeline.name));
        tree.add_empty_child("rx".to_string());
        for function in &pipeline.functions {
            tree.add_empty_child(function.to_string());
        }
        tree.add_empty_child("tx".to_string());
        tree.end_child();
    }
    tree.end_child();

    tree.begin_child("Devices".to_string());
    if info.devices.is_empty() {
        tree.add_empty_child("(none)".to_owned());
    }

    for device in &info.devices {
        tree.begin_child(format!("Device {}:{}", device.r#type, device.name));

        tree.begin_child("input".to_string());
        if device.input_pipelines.is_empty() {
            tree.add_empty_child("(none)".to_owned());
        }

        for pipeline in &device.input_pipelines {
            tree.add_empty_child(format!("Pipeline {} (weight: {})", pipeline.name, pipeline.weight));
        }
        tree.end_child();

        tree.begin_child("output".to_string());
        if device.output_pipelines.is_empty() {
            tree.add_empty_child("(none)".to_owned());
        }

        for pipeline in &device.output_pipelines {
            tree.add_empty_child(format!("Pipeline {} (weight: {})", pipeline.name, pipeline.weight));
        }
        tree.end_child();

        tree.end_child();
    }
    tree.end_child();

    tree.end_child();

    let tree = tree.build();
    let _ = ptree::print_tree(&tree);
}

/// Live bytes held by a memory node: allocated minus freed, floored at zero.
fn node_live(node: &MemoryNode) -> u64 {
    node.balloc_size.saturating_sub(node.bfree_size)
}

/// Fills `totals[idx]` and every descendant's slot with its subtree total:
/// a node's own live bytes plus the live bytes of all its descendants.
///
/// Post-order, so each slot is computed exactly once regardless of how many
/// times the tree is walked afterwards.
fn compute_subtree_totals(nodes: &[MemoryNode], children_of: &[Vec<usize>], idx: usize, totals: &mut [u64]) {
    let mut total = node_live(&nodes[idx]);
    for &child in &children_of[idx] {
        compute_subtree_totals(nodes, children_of, child, totals);
        total = total.saturating_add(totals[child]);
    }

    totals[idx] = total;
}

/// Render one node and its children into the tree builder.
///
/// Children are ordered by subtree total descending so the heaviest
/// subtrees come first; zero-total subtrees are skipped. The node's own
/// live bytes are shown alongside the subtree total only when they differ
/// — a leaf, or a node whose children are all zero, gets one number.
fn render_memory_node(
    tree: &mut TreeBuilder,
    nodes: &[MemoryNode],
    idx: usize,
    children_of: &[Vec<usize>],
    totals: &[u64],
) {
    let node = &nodes[idx];
    let total = totals[idx];
    let own = node_live(node);

    let label = if own == total {
        format!("{} (Used: {})", node.name, ByteSize::b(total))
    } else {
        format!(
            "{} (Used: {}, own: {})",
            node.name,
            ByteSize::b(total),
            ByteSize::b(own)
        )
    };
    tree.begin_child(label);

    let mut children = children_of[idx].clone();
    children.sort_by_key(|&child| Reverse(totals[child]));
    for child in children {
        if totals[child] > 0 {
            render_memory_node(tree, nodes, child, children_of, totals);
        }
    }

    tree.end_child();
}

/// Cap on the number of context names shown in the "own bytes" rollup.
const MEMORY_ROLLUP_LIMIT: usize = 10;

/// Aggregates own live bytes and node count by context `name`, across the
/// whole flat `nodes` slice regardless of depth or parent.
///
/// Own bytes (not subtree totals) avoid double-counting a parent and its
/// children under the same name. Returns entries sorted by summed bytes
/// descending, name ascending on ties, capped at [`MEMORY_ROLLUP_LIMIT`].
fn rollup_by_name(nodes: &[MemoryNode]) -> Vec<(String, u64, usize)> {
    let mut totals: BTreeMap<&str, (u64, usize)> = BTreeMap::new();
    for node in nodes {
        let entry = totals.entry(node.name.as_str()).or_insert((0, 0));
        entry.0 = entry.0.saturating_add(node_live(node));
        entry.1 += 1;
    }

    let mut rows: Vec<(String, u64, usize)> = totals
        .into_iter()
        .map(|(name, (bytes, count))| (name.to_string(), bytes, count))
        .collect();
    rows.sort_by_key(|&(_, bytes, _)| Reverse(bytes));
    rows.truncate(MEMORY_ROLLUP_LIMIT);
    rows
}

/// Render the "own bytes" rollup as plain aligned child lines.
///
/// Nested under the memory tree rather than a `tabled` table: it is a
/// two-column list capped at [`MEMORY_ROLLUP_LIMIT`] rows inside a `ptree`
/// branch, and `tabled`'s box-drawing output has no ptree-prefix awareness
/// for a table embedded as a single multi-line child label.
fn add_memory_rollup(tree: &mut TreeBuilder, nodes: &[MemoryNode]) {
    let rows = rollup_by_name(nodes);
    let Some(name_width) = rows.iter().map(|(name, _, _)| name.chars().count()).max() else {
        return;
    };

    tree.begin_child("Top contexts by own bytes:".to_string());
    for (name, bytes, count) in &rows {
        let size = ByteSize::b(*bytes);
        tree.add_empty_child(format!("{name:<name_width$}  {size:>10}  (×{count})"));
    }
    tree.end_child();
}

/// Render the memory-context tree under a `Memory tree:` branch.
fn add_memory_tree(tree: &mut TreeBuilder, nodes: &[MemoryNode]) {
    let mut children_of: Vec<Vec<usize>> = vec![Vec::new(); nodes.len()];
    let mut root_idx: Option<usize> = None;
    for (idx, node) in nodes.iter().enumerate() {
        if node.parent_idx == u32::MAX {
            root_idx = Some(idx);
        } else {
            let parent = node.parent_idx as usize;
            if parent < nodes.len() {
                children_of[parent].push(idx);
            }
        }
    }

    let Some(root) = root_idx else {
        return;
    };

    let mut totals = vec![0u64; nodes.len()];
    compute_subtree_totals(nodes, &children_of, root, &mut totals);

    tree.begin_child("Memory tree:".to_string());
    render_memory_node(tree, nodes, root, &children_of, &totals);
    add_memory_rollup(tree, nodes);
    tree.end_child();
}

#[cfg(test)]
mod test {
    use ynpb::pb::MemoryNode;

    use super::{compute_subtree_totals, node_live, rollup_by_name};

    fn node(name: &str, parent_idx: u32, balloc_size: u64, bfree_size: u64) -> MemoryNode {
        MemoryNode {
            name: name.to_string(),
            parent_idx,
            balloc_count: 0,
            bfree_count: 0,
            balloc_size,
            bfree_size,
        }
    }

    /// Builds the same parent -> children index map `add_memory_tree` does,
    /// for tests exercising `compute_subtree_totals`/`rollup_by_name` directly.
    fn children_of(nodes: &[MemoryNode]) -> Vec<Vec<usize>> {
        let mut children_of: Vec<Vec<usize>> = vec![Vec::new(); nodes.len()];
        for (idx, node) in nodes.iter().enumerate() {
            if node.parent_idx != u32::MAX {
                children_of[node.parent_idx as usize].push(idx);
            }
        }
        children_of
    }

    #[test]
    fn subtree_total_equals_sum_of_own_bytes() {
        let nodes = vec![
            node("root", u32::MAX, 100, 0),
            node("filter", 0, 50, 0),
            node("lpm", 0, 30, 10),
            node("value_table", 2, 15, 5),
        ];
        let children_of = children_of(&nodes);
        let mut totals = vec![0u64; nodes.len()];
        compute_subtree_totals(&nodes, &children_of, 0, &mut totals);

        let sum_of_own: u64 = nodes.iter().map(node_live).sum();
        assert_eq!(sum_of_own, totals[0]);
        assert_eq!(180, totals[0]);
    }

    #[test]
    fn rollup_groups_by_name_across_depths() {
        let nodes = vec![
            node("root", u32::MAX, 100, 0),
            node("filter", 0, 500, 10), // own 490, first filter
            node("lpm", 1, 20, 0),      // own 20, under the first filter
            node("filter", 0, 20, 10),  // own 10, second filter at the same depth
            node("lpm", 3, 30, 0),      // own 30, under the second filter
        ];

        let rows = rollup_by_name(&nodes);

        let filter = rows
            .iter()
            .find(|(name, ..)| name == "filter")
            .expect("filter row present");
        assert_eq!(500, filter.1);
        assert_eq!(2, filter.2);

        let lpm = rows.iter().find(|(name, ..)| name == "lpm").expect("lpm row present");
        assert_eq!(50, lpm.1);
        assert_eq!(2, lpm.2);

        // filter (500) outranks root (100) outranks lpm (50).
        assert_eq!("filter", rows[0].0);
    }
}
