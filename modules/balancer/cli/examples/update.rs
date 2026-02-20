//! Example: update command output
//! Run with: cargo run --example update [type] [format]
//! Where type is one of: created, updated (default: both types, all formats)
//! Where format is one of: table, tree, json (default: all formats)

mod common;

use std::{env, error::Error};

use yanet_cli_balancer::output::{self, OutputFormat};

fn main() -> Result<(), Box<dyn Error>> {
    let args: Vec<String> = env::args().collect();
    let update_type = args.get(1).map(|s| s.as_str());
    let format = args.get(2).map(|s| s.as_str());

    match (update_type, format) {
        // Specific type and format
        (Some("created"), Some("table")) => {
            let update_info = common::create_update_info_created_example();
            output::print_update_info(&update_info, OutputFormat::Table)?;
        }
        (Some("created"), Some("tree")) => {
            let update_info = common::create_update_info_created_example();
            output::print_update_info(&update_info, OutputFormat::Tree)?;
        }
        (Some("created"), Some("json")) => {
            let update_info = common::create_update_info_created_example();
            output::print_update_info(&update_info, OutputFormat::Json)?;
        }
        (Some("updated"), Some("table")) => {
            let update_info = common::create_update_info_updated_example();
            output::print_update_info(&update_info, OutputFormat::Table)?;
        }
        (Some("updated"), Some("tree")) => {
            let update_info = common::create_update_info_updated_example();
            output::print_update_info(&update_info, OutputFormat::Tree)?;
        }
        (Some("updated"), Some("json")) => {
            let update_info = common::create_update_info_updated_example();
            output::print_update_info(&update_info, OutputFormat::Json)?;
        }
        // Specific type, all formats
        (Some("created"), None) => {
            let update_info = common::create_update_info_created_example();

            println!("=== update (created): Table format ===\n");
            output::print_update_info(&update_info, OutputFormat::Table)?;

            println!("\n\n=== update (created): Tree format ===\n");
            output::print_update_info(&update_info, OutputFormat::Tree)?;

            println!("\n\n=== update (created): JSON format ===\n");
            output::print_update_info(&update_info, OutputFormat::Json)?;
        }
        (Some("updated"), None) => {
            let update_info = common::create_update_info_updated_example();

            println!("=== update (updated): Table format ===\n");
            output::print_update_info(&update_info, OutputFormat::Table)?;

            println!("\n\n=== update (updated): Tree format ===\n");
            output::print_update_info(&update_info, OutputFormat::Tree)?;

            println!("\n\n=== update (updated): JSON format ===\n");
            output::print_update_info(&update_info, OutputFormat::Json)?;
        }
        // Unknown type
        (Some(other), _) => {
            eprintln!("Unknown type: {}. Use: created or updated", other);
            std::process::exit(1);
        }
        // Unknown format
        (_, Some(other)) => {
            eprintln!("Unknown format: {}. Use: table, tree, or json", other);
            std::process::exit(1);
        }
        // No arguments - show all types and all formats
        (None, None) => {
            // Created scenario
            let update_info_created = common::create_update_info_created_example();

            println!("=== update (created): Table format ===\n");
            output::print_update_info(&update_info_created, OutputFormat::Table)?;

            println!("\n\n=== update (created): Tree format ===\n");
            output::print_update_info(&update_info_created, OutputFormat::Tree)?;

            println!("\n\n=== update (created): JSON format ===\n");
            output::print_update_info(&update_info_created, OutputFormat::Json)?;

            // Updated scenario
            let update_info_updated = common::create_update_info_updated_example();

            println!("\n\n=== update (updated): Table format ===\n");
            output::print_update_info(&update_info_updated, OutputFormat::Table)?;

            println!("\n\n=== update (updated): Tree format ===\n");
            output::print_update_info(&update_info_updated, OutputFormat::Tree)?;

            println!("\n\n=== update (updated): JSON format ===\n");
            output::print_update_info(&update_info_updated, OutputFormat::Json)?;
        }
    }

    Ok(())
}
