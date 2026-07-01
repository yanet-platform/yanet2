use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use dscppb::{
    AddPrefixesRequest, DscpConfig, RemovePrefixesRequest, SetDscpMarkingRequest, ShowConfigRequest,
    ShowConfigResponse, dscp_service_client::DscpServiceClient,
};
use netip::{Contiguous, IpNetwork};
use ptree::TreeBuilder;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};

use crate::dscppb::ListConfigsRequest;

#[allow(non_snake_case)]
pub mod dscppb {
    use serde::Serialize;

    tonic::include_proto!("modules.dscp.controlplane.dscppb.v1");
}

/// DSCP module for packet marking.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    List,
    Show(ShowConfigCmd),
    PrefixAdd(AddPrefixesCmd),
    PrefixRemove(RemovePrefixesCmd),
    SetMarking(SetDscpMarkingCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// DSCP module name to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct AddPrefixesCmd {
    /// DSCP module name to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// Prefix to be added to the input filter of the DSCP module.
    #[arg(long, short, required = true)]
    pub prefix: Vec<Contiguous<IpNetwork>>,
}

#[derive(Debug, Clone, Parser)]
pub struct RemovePrefixesCmd {
    /// DSCP module name to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// Prefix to be removed from the input filter of the DSCP module.
    #[arg(long, short, required = true)]
    pub prefix: Vec<Contiguous<IpNetwork>>,
}

#[derive(Debug, Clone, Parser)]
pub struct SetDscpMarkingCmd {
    /// DSCP module name to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// DSCP marking flag: 0 - Never, 1 - Default (only if original DSCP is 0),
    /// 2 - Always
    #[arg(long)]
    pub flag: u32,
    /// DSCP mark value (0-63)
    #[arg(long)]
    pub mark: u32,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.dscp.controlplane.dscppb.v1.DscpService";

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
    let mut service = DscpService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::PrefixAdd(cmd) => service.add_prefixes(cmd).await,
        ModeCmd::PrefixRemove(cmd) => service.remove_prefixes(cmd).await,
        ModeCmd::SetMarking(cmd) => service.set_dscp_marking(cmd).await,
    }
}

pub struct DscpService {
    service: Service<DscpServiceClient<LayeredChannel>>,
}

impl DscpService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
            DscpServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let request = ListConfigsRequest {};
        log::trace!("list configs request: {request:?}");
        let response = self
            .service
            .client()
            .list_configs(request)
            .await
            .map_err(self.service.status("list"))?
            .into_inner();
        log::debug!("list configs response: {response:?}");

        output::data(
            &response.configs,
            response.configs.is_empty(),
            format_args!("no dscp configs"),
            || {
                let mut tree = TreeBuilder::new("List DSCP Configs".to_string());
                for config in &response.configs {
                    tree.add_empty_child(config.clone());
                }
                let _ = ptree::print_tree(&tree.build());
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.to_owned() };
        log::trace!("show config request: {request:?}");
        let response = self
            .service
            .client()
            .show_config(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();
        log::debug!("show config response: {response:?}");

        output::data(&response, false, format_args!(""), || print_tree(&response));

        Ok(())
    }

    pub async fn add_prefixes(&mut self, cmd: AddPrefixesCmd) -> Result<(), Error> {
        let request = AddPrefixesRequest {
            name: cmd.config_name.clone(),
            prefixes: cmd.prefix.iter().map(|p| p.to_string()).collect(),
        };
        log::trace!("AddPrefixesRequest: {request:?}");
        let response = self
            .service
            .client()
            .add_prefixes(request)
            .await
            .map_err(self.service.status("prefix-add"))?
            .into_inner();
        log::debug!("AddPrefixesResponse: {response:?}");

        output::success(
            "prefix-add",
            format_args!("Added {} prefix(es) to {}.", cmd.prefix.len(), cmd.config_name),
        );

        Ok(())
    }

    pub async fn remove_prefixes(&mut self, cmd: RemovePrefixesCmd) -> Result<(), Error> {
        let request = RemovePrefixesRequest {
            name: cmd.config_name.clone(),
            prefixes: cmd.prefix.iter().map(|p| p.to_string()).collect(),
        };
        log::trace!("RemovePrefixesRequest: {request:?}");
        let response = self
            .service
            .client()
            .remove_prefixes(request)
            .await
            .map_err(self.service.status("prefix-remove"))?
            .into_inner();
        log::debug!("RemovePrefixesResponse: {response:?}");

        output::success(
            "prefix-remove",
            format_args!("Removed {} prefix(es) from {}.", cmd.prefix.len(), cmd.config_name),
        );

        Ok(())
    }

    pub async fn set_dscp_marking(&mut self, cmd: SetDscpMarkingCmd) -> Result<(), Error> {
        // Validate flag value
        if cmd.flag > 2 {
            return Err(self
                .service
                .invalid("set-marking", "Invalid flag value (must be 0, 1, or 2)"));
        }

        // Validate mark value (6-bit field)
        if cmd.mark > 63 {
            return Err(self.service.invalid("set-marking", "Invalid mark value (must be 0-63)"));
        }

        let request = SetDscpMarkingRequest {
            name: cmd.config_name.clone(),
            dscp_config: Some(DscpConfig { flag: cmd.flag, mark: cmd.mark }),
        };
        log::trace!("SetDscpMarkingRequest: {request:?}");
        let response = self
            .service
            .client()
            .set_dscp_marking(request)
            .await
            .map_err(self.service.status("set-marking"))?
            .into_inner();
        log::debug!("SetDscpMarkingResponse: {response:?}");

        output::success("set-marking", format_args!("Set DSCP marking on {}.", cmd.config_name));

        Ok(())
    }
}

fn print_tree(response: &ShowConfigResponse) {
    let mut tree = TreeBuilder::new("View DSCP Config".to_string());

    if let Some(config) = &response.config {
        if let Some(dscp_config) = config.dscp_config {
            tree.begin_child("DSCP Marking".to_string());
            tree.add_empty_child(format!("Flag: {}", flag_to_string(dscp_config.flag)));
            tree.add_empty_child(format!("Mark: {} (0x{:02x})", dscp_config.mark, dscp_config.mark));
            tree.end_child();
        }

        tree.begin_child("Prefixes".to_string());
        for (idx, prefix) in config.prefixes.iter().enumerate() {
            tree.add_empty_child(format!("{idx}: {prefix}"));
        }
        tree.end_child();
    }

    let _ = ptree::print_tree(&tree.build());
}

fn flag_to_string(flag: u32) -> String {
    match flag {
        0 => "Never".to_string(),
        1 => "Default (only if original DSCP is 0)".to_string(),
        2 => "Always".to_string(),
        _ => format!("Unknown ({flag})"),
    }
}
