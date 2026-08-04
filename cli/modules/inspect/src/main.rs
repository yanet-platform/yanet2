//! CLI for YANET "inspect" module.

use core::cmp::Reverse;

use bytesize::ByteSize;
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
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
#[command(version, about)]
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

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
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
        let service = Service::connect(connection, INSPECT_SERVICE, |channel| {
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

/// Subtree total: a node's own live bytes plus the live bytes of all its
/// descendants.
fn subtree_live(nodes: &[MemoryNode], children_of: &[Vec<usize>], idx: usize) -> u64 {
    let self_live = node_live(&nodes[idx]);
    let children_total: u64 = children_of[idx]
        .iter()
        .map(|&child| subtree_live(nodes, children_of, child))
        .sum();

    self_live.saturating_add(children_total)
}

/// Render one node and its children into the tree builder.
///
/// Children are ordered by subtree total descending so the heaviest
/// subtrees come first; zero-total subtrees are skipped.
fn render_memory_node(tree: &mut TreeBuilder, nodes: &[MemoryNode], idx: usize, children_of: &[Vec<usize>]) {
    let node = &nodes[idx];
    let total = subtree_live(nodes, children_of, idx);
    tree.begin_child(format!("{} (Used: {})", node.name, ByteSize::b(total)));

    let mut children = children_of[idx].clone();
    children.sort_by_key(|&child| Reverse(subtree_live(nodes, children_of, child)));
    for child in children {
        if subtree_live(nodes, children_of, child) > 0 {
            render_memory_node(tree, nodes, child, children_of);
        }
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

    tree.begin_child("Memory tree:".to_string());
    render_memory_node(tree, nodes, root, &children_of);
    tree.end_child();
}
