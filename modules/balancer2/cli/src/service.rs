use std::{error::Error, net::IpAddr, time};

use ptree::TreeBuilder;
use tonic::codec::CompressionEncoding;
use yanet_cli_balancer2::balancerpb::{
    self, GetConfigRequest, GetMetricsRequest, GetStateRequest, ListConfigsRequest, ListSessionsRequest,
    ListSessionsStatesRequest, PacketHandlerRef, RealUpdate, UpdateConfigRequest, UpdateRealsRequest,
    UpdateSessionsStateRequest, balancer_client::BalancerClient,
};
use ync::client::{ConnectionArgs, LayeredChannel};

use crate::{
    ConfigCmd, MetricsCmd, ModeCmd, ShowCmd, UpdateCmd, VsId,
    config::{BalancerConfig, ConfigParts},
    display, ip_to_bytes,
    reals::{DisableRealCmd, EnableRealCmd, RealsMode},
    sessions::{SessionsMode, SessionsShowCmd, SessionsUpdateCmd},
};

pub struct Balancer2Service {
    client: BalancerClient<LayeredChannel>,
}

impl Balancer2Service {
    pub async fn connect(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = BalancerClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn handle(&mut self, mode: ModeCmd) -> Result<(), Box<dyn Error>> {
        match mode {
            ModeCmd::Update(cmd) => self.update(cmd).await,
            ModeCmd::List => self.list().await,
            ModeCmd::Config(cmd) => self.config(cmd).await,
            ModeCmd::Show(cmd) => self.show(cmd).await,
            ModeCmd::Sessions(cmd) => match cmd.mode {
                SessionsMode::List => self.sessions_list().await,
                SessionsMode::Show(cmd) => self.sessions_show(cmd).await,
                SessionsMode::Update(cmd) => self.sessions_update(cmd).await,
            },
            ModeCmd::Metrics(cmd) => self.metrics(cmd).await,
            ModeCmd::Reals(cmd) => match cmd.mode {
                RealsMode::Enable(cmd) => self.enable_real(cmd).await,
                RealsMode::Disable(cmd) => self.disable_real(cmd).await,
            },
        }
    }

    async fn update(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let yaml_config = BalancerConfig::from_yaml_file(&cmd.config)?;
        let parts: ConfigParts = yaml_config.try_into()?;

        let request = UpdateConfigRequest {
            config_name: cmd.name.clone(),
            sessions_state_name: cmd.sessions,
            vs: parts.vs,
            timeouts: parts.timeouts,
            addr: parts.addr,
            wlc: parts.wlc,
        };
        log::trace!("update config request: {request:?}");

        let response = self.client.update_config(request).await?.into_inner();
        log::debug!("update config response: {response:?}");

        println!("balancer '{}' updated", cmd.name);
        Ok(())
    }

    async fn list(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        log::trace!("list configs request: {request:?}");

        let response = self.client.list_configs(request).await?.into_inner();
        log::debug!("list configs response: {response:?}");

        let mut tree = TreeBuilder::new("Balancers".to_string());
        for name in &response.names {
            tree.add_empty_child(name.clone());
        }
        let tree = tree.build();
        ptree::print_tree(&tree)?;

        Ok(())
    }

    async fn config(&mut self, cmd: ConfigCmd) -> Result<(), Box<dyn Error>> {
        let request = GetConfigRequest { config_name: cmd.name };
        log::trace!("get config request: {request:?}");

        let response = self.client.get_config(request).await?.into_inner();
        log::debug!("get config response: {response:?}");

        let mut json_value = serde_json::to_value(&response)?;
        display::prettify_json(&mut json_value);
        let yaml = serde_yaml::to_string(&json_value)?;
        print!("{yaml}");

        Ok(())
    }

    async fn show(&mut self, cmd: ShowCmd) -> Result<(), Box<dyn Error>> {
        let opts = display::ShowOptions {
            stats: cmd.stats || cmd.detail,
            acl: cmd.acl || cmd.detail,
            peers: cmd.peers || cmd.detail,
            decap: cmd.decap || cmd.detail,
        };

        let packet_handler_ref =
            if cmd.device.is_some() || cmd.pipeline.is_some() || cmd.function.is_some() || cmd.chain.is_some() {
                Some(PacketHandlerRef {
                    device: cmd.device,
                    pipeline: cmd.pipeline,
                    function: cmd.function,
                    chain: cmd.chain,
                })
            } else {
                None
            };

        let filter = cmd.filter.to_proto();
        let request = GetStateRequest {
            config_name: cmd.name,
            packet_handler_ref,
            filter,
        };
        log::trace!("get state request: {request:?}");

        let response = self.client.get_state(request).await?.into_inner();
        log::debug!("get state response: {response:?}");

        if response.states.is_empty() {
            log::info!("no balancer state found");
            return Ok(());
        }

        display::print_table_view(&response.states, &opts);

        Ok(())
    }

    async fn sessions_list(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListSessionsStatesRequest {};
        log::trace!("list sessions states request: {request:?}");

        let response = self.client.list_sessions_states(request).await?.into_inner();
        log::debug!("list sessions states response: {response:?}");

        let mut tree = TreeBuilder::new("Sessions States".to_string());
        for name in &response.names {
            tree.add_empty_child(name.clone());
        }
        let tree = tree.build();
        ptree::print_tree(&tree)?;

        Ok(())
    }

    async fn sessions_show(&mut self, cmd: SessionsShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ListSessionsRequest {
            sessions_state_name: cmd.name,
            filter: cmd.filter.to_proto(),
        };
        log::trace!("list sessions request: {request:?}");

        let mut stream = self.client.list_sessions(request).await?.into_inner();

        display::print_sessions_header();
        let now = time::SystemTime::now()
            .duration_since(time::UNIX_EPOCH)
            .expect("system clock before UNIX epoch")
            .as_secs() as i64;
        let mut printed = 0usize;
        while let Some(session) = stream.message().await? {
            display::print_session(&session, now);
            printed += 1;
        }

        if printed == 0 {
            log::info!("no sessions");
        }

        Ok(())
    }

    async fn sessions_update(&mut self, cmd: SessionsUpdateCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdateSessionsStateRequest {
            sessions_state_name: cmd.name.clone(),
            capacity: cmd.capacity,
        };
        log::trace!("update sessions state request: {request:?}");

        let response = self.client.update_sessions_state(request).await?.into_inner();
        log::debug!("update sessions state response: {response:?}");

        println!("sessions state '{}' updated (capacity: {})", cmd.name, cmd.capacity);
        Ok(())
    }

    async fn metrics(&mut self, _cmd: MetricsCmd) -> Result<(), Box<dyn Error>> {
        let request = GetMetricsRequest {};
        log::trace!("get metrics request: {request:?}");

        let response = self.client.get_metrics(request).await?.into_inner();
        log::debug!("get metrics response: {response:?}");

        let mut json_value = serde_json::to_value(&response)?;
        display::prettify_json(&mut json_value);
        let json = serde_json::to_string(&json_value)?;
        println!("{json}");

        Ok(())
    }

    async fn enable_real(&mut self, cmd: EnableRealCmd) -> Result<(), Box<dyn Error>> {
        let updates = build_real_updates(&cmd.vs, &cmd.reals, Some(true), cmd.weight);
        self.send_real_updates(cmd.name, updates).await
    }

    async fn disable_real(&mut self, cmd: DisableRealCmd) -> Result<(), Box<dyn Error>> {
        let updates = build_real_updates(&cmd.vs, &cmd.reals, Some(false), None);
        self.send_real_updates(cmd.name, updates).await
    }

    async fn send_real_updates(&mut self, config_name: String, updates: Vec<RealUpdate>) -> Result<(), Box<dyn Error>> {
        let request = UpdateRealsRequest {
            config_name: config_name.clone(),
            updates,
        };
        log::trace!("update reals request: {request:?}");

        let response = self.client.update_reals(request).await?.into_inner();
        log::debug!("update reals response: {response:?}");

        println!("balancer '{config_name}' reals updated");
        Ok(())
    }
}

fn build_real_updates(vs: &VsId, reals: &[IpAddr], enable: Option<bool>, weight: Option<u32>) -> Vec<RealUpdate> {
    let vs_id: balancerpb::VsIdentifier = vs.into();

    reals
        .iter()
        .map(|real_ip| RealUpdate {
            real_id: Some(balancerpb::RealIdentifier {
                vs: Some(vs_id.clone()),
                real: Some(balancerpb::RelativeRealIdentifier { ip: ip_to_bytes(*real_ip), port: 0 }),
            }),
            enable,
            weight,
        })
        .collect()
}
