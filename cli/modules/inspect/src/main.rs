//! CLI for YANET "inspect" module.

use core::{error::Error, ops::ControlFlow};

use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::CompleteEnv;
use code::{inspect_service_client::InspectServiceClient, InspectRequest};
use ptree::TreeBuilder;
use tonic::transport::Channel;
use ync::logging;

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("ynpb");
}

/// Inspect module - displays system introspection information.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Output format.
    #[clap(long, value_enum, default_value_t = OutputFormat::Tree)]
    pub format: OutputFormat,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

/// Output format options.
#[derive(Debug, Clone, ValueEnum)]
pub enum OutputFormat {
    /// Tree structure with colored output (default).
    Tree,
    /// JSON format.
    Json,
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("no error expected");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = InspectService::new(cmd.endpoint).await?;

    match cmd.format {
        OutputFormat::Json => service.show_json().await?,
        OutputFormat::Tree => service.show_tree().await?,
    }

    Ok(())
}

pub struct InspectService {
    client: InspectServiceClient<Channel>,
}

impl InspectService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = InspectServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    pub async fn show_json(&mut self) -> Result<(), Box<dyn Error>> {
        let request = InspectRequest {};
        let response = self.client.inspect(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }

    pub async fn show_tree(&mut self) -> Result<(), Box<dyn Error>> {
        let request = InspectRequest {};
        let response = self.client.inspect(request).await?;
        self.format_tree_output(response.get_ref())?;
        Ok(())
    }

    fn format_tree_output(&self, response: &code::InspectResponse) -> Result<(), Box<dyn Error>> {
        let mut numa_nodes = Vec::new();
        traverse_bits(response.numa_bitmap as u64, |idx| -> ControlFlow<()> {
            numa_nodes.push(idx);
            ControlFlow::Continue(())
        });

        let mut tree = TreeBuilder::new("YANET System".to_string());

        for info in &response.numa_info {
            tree.begin_child(format!("NUMA {}", info.numa));

            tree.begin_child("Dataplane Modules".to_string());
            for (idx, module) in info.dp_modules.iter().enumerate() {
                tree.add_empty_child(format!("{}: {}", idx, module.name));
            }
            tree.end_child();

            tree.begin_child("Controlplane Configurations".to_string());
            for cfg in &info.cp_configs {
                let module = &info.dp_modules[cfg.module_idx as usize];
                tree.add_empty_child(format!("{}:{} (gen: {})", module.name, cfg.name, cfg.gen));
            }
            tree.end_child();

            tree.begin_child("Agents".to_string());
            for agent in &info.agents {
                tree.begin_child(format!("{}", agent.name));

                for instance in &agent.instances {
                    tree.begin_child(format!("Instance (PID: {})", instance.pid));
                    tree.add_empty_child(format!("Memory limit: {}", instance.memory_limit));
                    tree.add_empty_child(format!("Allocated: {}", instance.allocated));
                    tree.add_empty_child(format!("Freed: {}", instance.freed));
                    tree.add_empty_child(format!("Generation: {}", instance.gen));
                    tree.end_child();
                }

                tree.end_child();
            }
            tree.end_child();

            tree.begin_child("Pipelines".to_string());
            for pipeline in &info.pipelines {
                tree.begin_child(format!("Pipeline {}", pipeline.pipeline_id));
                tree.add_empty_child("rx".to_string());
                for stage in &pipeline.modules {
                    let cp_cfg = &info.cp_configs[stage.config_index as usize];
                    let dp_cfg = &info.dp_modules[cp_cfg.module_idx as usize];
                    tree.add_empty_child(format!("{}:{}", dp_cfg.name, cp_cfg.name));
                }
                tree.add_empty_child("tx".to_string());
                tree.end_child();
            }
            tree.end_child();

            tree.end_child();
        }

        let tree = tree.build();
        ptree::print_tree(&tree)?;

        Ok(())
    }
}

#[inline]
pub fn traverse_bits<F, B>(mut word: u64, mut f: F) -> ControlFlow<B>
where
    F: FnMut(usize) -> ControlFlow<B>,
{
    while word > 0 {
        let r = word.trailing_zeros() as usize;
        let t = word & word.wrapping_neg();
        word ^= t;

        f(r)?;
    }

    ControlFlow::Continue(())
}
