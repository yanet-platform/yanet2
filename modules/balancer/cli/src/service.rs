use std::error::Error;

use ptree::TreeBuilder;
use tonic::codec::CompressionEncoding;
use yanet_cli_balancer::balancerpb::{
    self, FlushRealsRequest, GetConfigRequest, GetMetricsRequest, GetStateRequest, ListBalancersRequest,
    ListSessionsRequest, PacketHandlerRef, RealUpdate, SetConfigRequest, UpdateRealsRequest,
    balancer_client::BalancerClient,
};
use ync::client::{ConnectionArgs, LayeredChannel};

use crate::{
    ConfigCmd, DisableRealCmd, EnableRealCmd, FlushRealsCmd, MetricsCmd, ModeCmd, SessionsCmd, ShowCmd, UpdateCmd,
    config::BalancerConfig, display, ip_to_bytes, parse_vs_identifier,
};

pub struct BalancerService {
    client: BalancerClient<LayeredChannel>,
}

impl BalancerService {
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
            ModeCmd::Sessions(cmd) => self.sessions(cmd).await,
            ModeCmd::Metrics(cmd) => self.metrics(cmd).await,
            ModeCmd::Reals(cmd) => match cmd.mode {
                crate::RealsMode::Enable(cmd) => self.enable_real(cmd).await,
                crate::RealsMode::Disable(cmd) => self.disable_real(cmd).await,
                crate::RealsMode::Flush(cmd) => self.flush_reals(cmd).await,
            },
        }
    }

    async fn update(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let yaml_config = BalancerConfig::from_yaml_file(&cmd.config)?;
        let proto_config: balancerpb::BalancerConfig = yaml_config.try_into()?;

        let request = SetConfigRequest {
            name: cmd.name.clone(),
            config: Some(proto_config),
        };
        log::trace!("set config request: {request:?}");

        let response = self.client.set_config(request).await?.into_inner();
        log::debug!("set config response: {response:?}");

        log::info!("Balancer '{}' updated successfully", response.name);
        if let Some(reuse) = &response.reuse {
            log::info!(
                "Reuse: ipv4_vs_matcher={}, ipv6_vs_matcher={}, ipv4_decap={}, ipv6_decap={}",
                reuse.ipv4_vs_matcher_reused,
                reuse.ipv6_vs_matcher_reused,
                reuse.ipv4_decap_filter_reused,
                reuse.ipv6_decap_filter_reused,
            );
            for vs_reuse in &reuse.vs_reuse_reports {
                if let Some(id) = &vs_reuse.vs_identifier {
                    let ip = crate::bytes_to_ip(&id.addr)
                        .map(|a| a.to_string())
                        .unwrap_or_else(|_| "?".to_string());
                    let proto = match balancerpb::TransportProto::try_from(id.proto) {
                        Ok(balancerpb::TransportProto::Tcp) => "tcp",
                        Ok(balancerpb::TransportProto::Udp) => "udp",
                        _ => "?",
                    };
                    log::info!(
                        "  VS {}:{}/{}: acl_reused={}, selector_reused={}",
                        ip,
                        id.port,
                        proto,
                        vs_reuse.acl_reused,
                        vs_reuse.selector_reused,
                    );
                }
            }
        }
        if response.session_table_capacity > 0 {
            log::info!("Session table capacity: {}", response.session_table_capacity);
        }

        Ok(())
    }

    async fn list(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListBalancersRequest {};
        log::trace!("list balancers request: {request:?}");

        let response = self.client.list_balancers(request).await?.into_inner();
        log::debug!("list balancers response: {response:?}");

        let mut tree = TreeBuilder::new("Balancers".to_string());
        for name in &response.names {
            tree.add_empty_child(name.clone());
        }
        let tree = tree.build();
        ptree::print_tree(&tree)?;

        Ok(())
    }

    async fn config(&mut self, cmd: ConfigCmd) -> Result<(), Box<dyn Error>> {
        let request = GetConfigRequest { name: cmd.name };
        log::trace!("get config request: {request:?}");

        let response = self.client.get_config(request).await?.into_inner();
        log::debug!("get config response: {response:?}");

        let mut yaml_value = serde_json::to_value(&response)?;
        display::prettify_config(&mut yaml_value);
        let yaml = serde_yaml::to_string(&yaml_value)?;
        print!("{yaml}");

        Ok(())
    }

    async fn show(&mut self, cmd: ShowCmd) -> Result<(), Box<dyn Error>> {
        let needs_table = cmd.needs_table();
        let include_counters = cmd.include_counters();

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

        let request = GetStateRequest {
            name: cmd.name,
            packet_handler_ref,
            filter: cmd.filter.to_proto(),
            include_counters,
        };
        log::trace!("get state request: {request:?}");

        let response = self.client.get_state(request).await?.into_inner();
        log::debug!("get state response: {response:?}");

        if response.state.is_empty() {
            log::info!("No balancer state found");
            return Ok(());
        }

        if needs_table {
            display::print_table_view(&response.state, &opts);
        } else {
            display::print_compact(&response.state[0]);
        }

        Ok(())
    }

    async fn sessions(&mut self, cmd: SessionsCmd) -> Result<(), Box<dyn Error>> {
        let request = ListSessionsRequest {
            name: cmd.name,
            filter: cmd.filter.to_proto(),
        };
        log::trace!("list sessions request: {request:?}");

        let mut stream = self.client.list_sessions(request).await?.into_inner();

        display::print_sessions_header();
        while let Some(session) = stream.message().await? {
            display::print_session(&session);
        }

        Ok(())
    }

    async fn metrics(&mut self, _cmd: MetricsCmd) -> Result<(), Box<dyn Error>> {
        let request = GetMetricsRequest {};
        log::trace!("get metrics request: {request:?}");

        let response = self.client.get_metrics(request).await?.into_inner();
        log::debug!("get metrics response: {response:?}");

        let mut json_value = serde_json::to_value(&response)?;
        display::prettify_config(&mut json_value);
        let json = serde_json::to_string_pretty(&json_value)?;
        println!("{json}");

        Ok(())
    }

    async fn enable_real(&mut self, cmd: EnableRealCmd) -> Result<(), Box<dyn Error>> {
        let (ip, port, proto) = parse_vs_identifier(&cmd.vs)?;
        let vs_id = balancerpb::VsIdentifier {
            addr: ip_to_bytes(ip),
            port: port as u32,
            proto: proto as i32,
        };

        let updates: Vec<RealUpdate> = cmd
            .reals
            .iter()
            .map(|r| {
                let real_ip: std::net::IpAddr = r.parse().map_err(|e| format!("invalid real IP '{}': {}", r, e))?;
                Ok(RealUpdate {
                    real_id: Some(balancerpb::RealIdentifier {
                        vs: Some(vs_id.clone()),
                        real: Some(balancerpb::RelativeRealIdentifier { ip: ip_to_bytes(real_ip), port: 0 }),
                    }),
                    enable: Some(true),
                    weight: cmd.weight,
                })
            })
            .collect::<Result<Vec<_>, String>>()?;

        let request = UpdateRealsRequest {
            name: cmd.name,
            updates,
            buffer: !cmd.flush,
        };
        log::trace!("update reals request: {request:?}");

        let response = self.client.update_reals(request).await?.into_inner();
        log::debug!("update reals response: {response:?}");

        if response.updates_buffered > 0 {
            log::info!(
                "Balancer '{}': {} updates buffered (use 'reals flush' to apply)",
                response.name,
                response.updates_buffered
            );
        }
        if response.updates_applied > 0 {
            log::info!(
                "Balancer '{}': {} updates applied",
                response.name,
                response.updates_applied
            );
        }

        Ok(())
    }

    async fn disable_real(&mut self, cmd: DisableRealCmd) -> Result<(), Box<dyn Error>> {
        let (ip, port, proto) = parse_vs_identifier(&cmd.vs)?;
        let vs_id = balancerpb::VsIdentifier {
            addr: ip_to_bytes(ip),
            port: port as u32,
            proto: proto as i32,
        };

        let updates: Vec<RealUpdate> = cmd
            .reals
            .iter()
            .map(|r| {
                let real_ip: std::net::IpAddr = r.parse().map_err(|e| format!("invalid real IP '{}': {}", r, e))?;
                Ok(RealUpdate {
                    real_id: Some(balancerpb::RealIdentifier {
                        vs: Some(vs_id.clone()),
                        real: Some(balancerpb::RelativeRealIdentifier { ip: ip_to_bytes(real_ip), port: 0 }),
                    }),
                    enable: Some(false),
                    weight: None,
                })
            })
            .collect::<Result<Vec<_>, String>>()?;

        let request = UpdateRealsRequest {
            name: cmd.name,
            updates,
            buffer: !cmd.flush,
        };
        log::trace!("update reals request: {request:?}");

        let response = self.client.update_reals(request).await?.into_inner();
        log::debug!("update reals response: {response:?}");

        if response.updates_buffered > 0 {
            log::info!(
                "Balancer '{}': {} updates buffered (use 'reals flush' to apply)",
                response.name,
                response.updates_buffered
            );
        }
        if response.updates_applied > 0 {
            log::info!(
                "Balancer '{}': {} updates applied",
                response.name,
                response.updates_applied
            );
        }

        Ok(())
    }

    async fn flush_reals(&mut self, cmd: FlushRealsCmd) -> Result<(), Box<dyn Error>> {
        let request = FlushRealsRequest { name: cmd.name };
        log::trace!("flush reals request: {request:?}");

        let response = self.client.flush_reals(request).await?.into_inner();
        log::debug!("flush reals response: {response:?}");

        log::info!(
            "Balancer '{}': {} updates flushed",
            response.name,
            response.updates_flushed
        );

        Ok(())
    }
}
