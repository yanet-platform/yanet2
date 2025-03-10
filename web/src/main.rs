use leptos::prelude::*;
use yanetweb::App;

fn main() {
    _ = console_log::init_with_level(log::Level::Debug);

    leptos::mount::mount_to_body(|| {
        view! { <App /> }
    })
}
