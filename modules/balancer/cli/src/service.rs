//! gRPC service client implementation

use std::error::Error;

use tonic::codec::CompressionEncoding;
use ync::client::{ConnectionArgs, LayeredChannel};

use crate::{
    cmd::*,
    entities::{BalancerConfig, VsListConfig},
    output,
    rpc::{BalancerServiceClient, balancerpb},
};

////////////////////////////////////////////////////////////////////////////////
// Logging macros with custom target
////////////////////////////////////////////////////////////////////////////////

macro_rules! info {
    ($($arg:tt)*) => {
        log::info!(target: "yanet_cli_balancer", $($arg)*)
    };
}

////////////////////////////////////////////////////////////////////////////////
// Service
////////////////////////////////////////////////////////////////////////////////

pub struct BalancerService {
    client: BalancerServiceClient<LayeredChannel>,
}

impl BalancerService {
    /// Connect to the gRPC endpoint
    pub async fn connect(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = BalancerServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    /// Handle the command
    pub async fn handle_cmd(&mut self, mode: Mode) -> Result<(), Box<dyn Error>> {
        log::trace!("Handling command: {:?}", mode);

        match mode {
            Mode::Update(cmd) => self.update_config(cmd).await,
            Mode::Reals(cmd) => self.handle_reals(cmd).await,
            Mode::Vs(cmd) => self.handle_vs(cmd).await,
            Mode::Config(cmd) => self.config(cmd).await,
            Mode::List(cmd) => self.list(cmd).await,
            Mode::Stats(cmd) => self.stats(cmd).await,
            Mode::Info(cmd) => self.info(cmd).await,
            Mode::Sessions(cmd) => self.sessions(cmd).await,
            Mode::Graph(cmd) => self.graph(cmd).await,
            Mode::Inspect(cmd) => self.inspect(cmd).await,
            Mode::Metrics(cmd) => self.metrics(cmd).await,
        }
    }

    /// Update balancer configuration
    async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        info!("Loading configuration from: {}", cmd.config);

        let config = BalancerConfig::from_yaml_file(&cmd.config)?;
        let balancer_config: balancerpb::BalancerConfig = config.try_into()?;

        let request = balancerpb::UpdateConfigRequest {
            name: cmd.name.clone(),
            config: Some(balancer_config),
        };

        log::debug!("Sending UpdateConfig request");
        let response = self.client.update_config(request).await?.into_inner();

        info!("Successfully updated configuration for '{}'", cmd.name);

        // Display update information if available
        if let Some(update_info) = &response.update_info {
            output::print_update_info(update_info, cmd.format.to_format())?;
        }

        Ok(())
    }

    /// Handle reals commands
    async fn handle_reals(&mut self, cmd: RealsCmd) -> Result<(), Box<dyn Error>> {
        match cmd.mode {
            RealsMode::Enable(cmd) => self.enable_real(cmd).await,
            RealsMode::Disable(cmd) => self.disable_real(cmd).await,
            RealsMode::Flush(cmd) => self.flush_real_updates(cmd).await,
        }
    }

    /// Enable a real server
    async fn enable_real(&mut self, cmd: EnableRealCmd) -> Result<(), Box<dyn Error>> {
        let flush = cmd.flush;
        let name = cmd.name.clone();

        let name_display = name.as_deref().unwrap_or("<auto>");
        info!(
            "Enabling {} real(s) of VS {} for '{}'",
            cmd.reals.len(),
            cmd.vs,
            name_display
        );

        let request: balancerpb::UpdateRealsRequest = cmd.try_into()?;

        log::debug!("Sending UpdateReals request");
        self.client.update_reals(request).await?;

        info!("Successfully buffered real enable");

        // If flush flag is set, immediately flush the updates
        if flush {
            let name_display = name.as_deref().unwrap_or("<auto>");
            info!("Flushing buffered real updates for '{}'", name_display);
            let flush_request = balancerpb::FlushRealUpdatesRequest { name };
            let response = self.client.flush_real_updates(flush_request).await?.into_inner();
            info!("Successfully flushed {} update(s)", response.updates_flushed);
        }

        Ok(())
    }

    /// Disable a real server
    async fn disable_real(&mut self, cmd: DisableRealCmd) -> Result<(), Box<dyn Error>> {
        let flush = cmd.flush;
        let name = cmd.name.clone();
        let reals_count = cmd.reals.len();

        let name_display = name.as_deref().unwrap_or("<auto>");
        info!(
            "Disabling {} real(s) of VS {} for '{}'",
            reals_count, cmd.vs, name_display
        );

        let request: balancerpb::UpdateRealsRequest = cmd.try_into()?;

        log::debug!("Sending UpdateReals request");
        self.client.update_reals(request).await?;

        info!("Successfully buffered real disable");

        // If flush flag is set, immediately flush the updates
        if flush {
            info!("Flushing buffered real updates");
            let flush_request = balancerpb::FlushRealUpdatesRequest { name };
            let response = self.client.flush_real_updates(flush_request).await?.into_inner();
            info!("Successfully flushed {} update(s)", response.updates_flushed);
        }

        Ok(())
    }

    /// Flush buffered real updates
    async fn flush_real_updates(&mut self, cmd: FlushRealUpdatesCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        info!("Flushing buffered real updates for '{}'", name_display);

        let request: balancerpb::FlushRealUpdatesRequest = cmd.into();

        log::debug!("Sending FlushRealUpdates request");
        let response = self.client.flush_real_updates(request).await?.into_inner();

        info!("Successfully flushed {} update(s)", response.updates_flushed);
        Ok(())
    }

    /// Show balancer configuration
    async fn config(&mut self, cmd: ConfigCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        log::debug!("Fetching configuration for '{}'", name_display);

        let request: balancerpb::ShowConfigRequest = (&cmd).into();
        let response = self.client.show_config(request).await?.into_inner();

        output::print_show_config(&response, cmd.format.to_format())?;
        Ok(())
    }

    /// List all balancer configurations
    async fn list(&mut self, cmd: ListCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching all configurations");

        let request = balancerpb::ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();

        output::print_list_configs(&response, cmd.format.to_format())?;
        Ok(())
    }

    /// Show configuration statistics
    async fn stats(&mut self, cmd: StatsCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        log::debug!("Fetching statistics for '{}'", name_display);

        let request: balancerpb::ShowStatsRequest = (&cmd).into();
        let response = self.client.show_stats(request).await?.into_inner();

        output::print_show_stats(&response, cmd.format.to_format())?;
        Ok(())
    }

    /// Show state information
    async fn info(&mut self, cmd: InfoCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        log::debug!("Fetching state info for '{}'", name_display);

        let request: balancerpb::ShowInfoRequest = (&cmd).into();
        let response = self.client.show_info(request).await?.into_inner();

        output::print_show_info(&response, cmd.format.to_format())?;
        Ok(())
    }

    /// Show sessions information
    async fn sessions(&mut self, cmd: SessionsCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        log::debug!("Fetching sessions info for '{}'", name_display);

        let request: balancerpb::ShowSessionsRequest = (&cmd).into();
        let response = self.client.show_sessions(request).await?.into_inner();

        output::print_show_sessions(&response, cmd.format.to_format())?;
        Ok(())
    }

    async fn graph(&mut self, cmd: GraphCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        log::debug!("Fetching graph info for '{}'", name_display);

        let request: balancerpb::ShowGraphRequest = (&cmd).into();
        let response = self.client.show_graph(request).await?.into_inner();

        output::print_show_graph(&response, cmd.format.to_format())?;
        Ok(())
    }

    /// Show memory usage inspection
    async fn inspect(&mut self, cmd: InspectCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching memory inspection");

        let request: balancerpb::ShowInspectRequest = (&cmd).into();
        let response = self.client.show_inspect(request).await?.into_inner();

        output::print_show_inspect(&response, cmd.format.to_format())?;
        Ok(())
    }

    /// Metrics
    async fn metrics(&mut self, cmd: MetricsCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching metrics");
        let request: balancerpb::GetMetricsRequest = (&cmd).into();
        let response = self.client.get_metrics(request).await?.into_inner();
        let s = serde_json::to_string(&response)?;
        println!("{}", s);
        Ok(())
    }

    /// Handle VS commands
    async fn handle_vs(&mut self, cmd: VsCmd) -> Result<(), Box<dyn Error>> {
        match cmd.mode {
            VsMode::Update(cmd) => self.update_vs(cmd).await,
            VsMode::Delete(cmd) => self.delete_vs(cmd).await,
        }
    }

    /// Update virtual services
    async fn update_vs(&mut self, cmd: UpdateVsCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        info!("Loading VS configuration from: {}", cmd.config);

        let vs_config = VsListConfig::from_yaml_file(&cmd.config)?;
        let vs_count = vs_config.vs.len();

        // Convert VirtualService entities to protobuf
        let vs_list: Result<Vec<balancerpb::VirtualService>, String> =
            vs_config.vs.into_iter().map(TryInto::try_into).collect();
        let vs_list = vs_list?;

        let request = balancerpb::UpdateVsRequest { name: cmd.name.clone(), vs: vs_list };

        log::debug!("Sending UpdateVS request for '{}'", name_display);
        let response = self.client.update_vs(request).await?.into_inner();

        info!(
            "Successfully updated {} virtual service(s) for '{}'",
            vs_count, response.name
        );

        // Display update information
        if let Some(update_info) = &response.info {
            output::print_vs_update_info(update_info, cmd.format.to_format(), output::VsOperation::Update)?;
        }

        Ok(())
    }

    /// Delete virtual services
    async fn delete_vs(&mut self, cmd: DeleteVsCmd) -> Result<(), Box<dyn Error>> {
        // Extract values before moving cmd
        let name_for_display = cmd.name.clone();
        let name_display = name_for_display.as_deref().unwrap_or("<auto>");
        let vs_count = cmd.vs.len();
        let format = cmd.format.to_format();

        info!("Deleting {} virtual service(s) from '{}'", vs_count, name_display);

        let request: balancerpb::DeleteVsRequest = cmd.try_into()?;

        log::debug!("Sending DeleteVS request for '{}'", name_display);
        let response = self.client.delete_vs(request).await?.into_inner();

        info!(
            "Successfully deleted {} virtual service(s) from '{}'",
            vs_count, response.name
        );

        // Display update information
        if let Some(update_info) = &response.info {
            output::print_vs_update_info(update_info, format, output::VsOperation::Delete)?;
        }

        Ok(())
    }
}
