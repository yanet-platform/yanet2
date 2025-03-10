use leptos::prelude::*;
use leptos_router::{
    components::{Route, Router, Routes},
    path,
};
use settings::SideBarContext;

// mod api;
mod components;
// mod i18n;
mod settings;
mod theme;
mod xxx;

use crate::{
    components::{common::snackbar::Snackbar, demo::DemoView, home::Home, nav::Nav, settings::SettingsView},
    theme::ThemeContext,
};

#[component]
pub fn App() -> impl IntoView {
    provide_context(ThemeContext::new());
    provide_context(SideBarContext::new());

    view! {
        <Snackbar>
            <div class="app">
                <Router>
                    <Nav />
                    <main>
                        <Routes fallback=|| "Not found.">
                            <Route path=path!("/") view=Home />
                            <Route path=path!("/demo") view=DemoView />
                            <Route path=path!("/settings") view=|| view! { <SettingsView /> } />
                        </Routes>
                    </main>
                </Router>
            </div>
        </Snackbar>
    }
}
