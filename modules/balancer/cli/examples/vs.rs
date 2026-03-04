//! Example: vs command output
//! Run with: cargo run --example vs [operation] [format]
//! Where operation is one of: update, delete (default: both operations, all formats)
//! Where format is one of: table, tree, json (default: all formats)

mod common;

use std::{env, error::Error};

use yanet_cli_balancer::output::{self, OutputFormat, VsOperation};

fn main() -> Result<(), Box<dyn Error>> {
    let args: Vec<String> = env::args().collect();
    let operation = args.get(1).map(|s| s.as_str());
    let format = args.get(2).map(|s| s.as_str());

    match (operation, format) {
        // Specific operation and format
        (Some("update"), Some("table")) => {
            let update_info = common::create_vs_update_info_example();
            output::print_vs_update_info(&update_info, OutputFormat::Table, VsOperation::Update)?;
        }
        (Some("update"), Some("tree")) => {
            let update_info = common::create_vs_update_info_example();
            output::print_vs_update_info(&update_info, OutputFormat::Tree, VsOperation::Update)?;
        }
        (Some("update"), Some("json")) => {
            let update_info = common::create_vs_update_info_example();
            output::print_vs_update_info(&update_info, OutputFormat::Json, VsOperation::Update)?;
        }
        (Some("delete"), Some("table")) => {
            let update_info = common::create_vs_delete_info_example();
            output::print_vs_update_info(&update_info, OutputFormat::Table, VsOperation::Delete)?;
        }
        (Some("delete"), Some("tree")) => {
            let update_info = common::create_vs_delete_info_example();
            output::print_vs_update_info(&update_info, OutputFormat::Tree, VsOperation::Delete)?;
        }
        (Some("delete"), Some("json")) => {
            let update_info = common::create_vs_delete_info_example();
            output::print_vs_update_info(&update_info, OutputFormat::Json, VsOperation::Delete)?;
        }
        // Specific operation, all formats
        (Some("update"), None) => {
            let update_info = common::create_vs_update_info_example();

            println!("=== vs update: Table format ===\n");
            output::print_vs_update_info(&update_info, OutputFormat::Table, VsOperation::Update)?;

            println!("\n\n=== vs update: Tree format ===\n");
            output::print_vs_update_info(&update_info, OutputFormat::Tree, VsOperation::Update)?;

            println!("\n\n=== vs update: JSON format ===\n");
            output::print_vs_update_info(&update_info, OutputFormat::Json, VsOperation::Update)?;
        }
        (Some("delete"), None) => {
            let update_info = common::create_vs_delete_info_example();

            println!("=== vs delete: Table format ===\n");
            output::print_vs_update_info(&update_info, OutputFormat::Table, VsOperation::Delete)?;

            println!("\n\n=== vs delete: Tree format ===\n");
            output::print_vs_update_info(&update_info, OutputFormat::Tree, VsOperation::Delete)?;

            println!("\n\n=== vs delete: JSON format ===\n");
            output::print_vs_update_info(&update_info, OutputFormat::Json, VsOperation::Delete)?;
        }
        // Unknown operation
        (Some(other), _) => {
            eprintln!("Unknown operation: {}. Use: update or delete", other);
            std::process::exit(1);
        }
        // Unknown format
        (_, Some(other)) => {
            eprintln!("Unknown format: {}. Use: table, tree, or json", other);
            std::process::exit(1);
        }
        // No arguments - show all operations and all formats
        (None, None) => {
            // Update operation
            let update_info = common::create_vs_update_info_example();

            println!("=== vs update: Table format ===\n");
            output::print_vs_update_info(&update_info, OutputFormat::Table, VsOperation::Update)?;

            println!("\n\n=== vs update: Tree format ===\n");
            output::print_vs_update_info(&update_info, OutputFormat::Tree, VsOperation::Update)?;

            println!("\n\n=== vs update: JSON format ===\n");
            output::print_vs_update_info(&update_info, OutputFormat::Json, VsOperation::Update)?;

            // Delete operation
            let delete_info = common::create_vs_delete_info_example();

            println!("\n\n=== vs delete: Table format ===\n");
            output::print_vs_update_info(&delete_info, OutputFormat::Table, VsOperation::Delete)?;

            println!("\n\n=== vs delete: Tree format ===\n");
            output::print_vs_update_info(&delete_info, OutputFormat::Tree, VsOperation::Delete)?;

            println!("\n\n=== vs delete: JSON format ===\n");
            output::print_vs_update_info(&delete_info, OutputFormat::Json, VsOperation::Delete)?;
        }
    }

    Ok(())
}
