use leptos::prelude::*;
use minify::MinifyButton;

use crate::{
    components::common::{
        icons::IconKind,
        nav::{MenuExtLink, MenuLink, NavFooter, NavHeader, NavMain, NavSecondary},
    },
    settings::SideBarContext,
};

mod minify;

#[component]
pub fn Nav() -> impl IntoView {
    let ctx: SideBarContext = expect_context();

    let title = move || {
        if ctx.is_compact() {
            Some("Expand")
        } else {
            Some("Collapse")
        }
    };

    view! {
        <nav class="nav" class:compact=move || ctx.is_compact()>
            <NavHeader name="YANET" icon=IconKind::Logo />
            <NavMain>
                <MenuLink name="Home" href="/" icon=IconKind::Home is_exact=true />
                <MenuLink name="Demo" href="/demo" icon=IconKind::Tool is_exact=true />
            </NavMain>
            <NavSecondary>
                <MenuLink name="Settings" href="/settings" icon=IconKind::Settings />
                <MenuExtLink
                    name="Documentation"
                    href="https://github.com/yanet-platform/yanet2"
                    icon=IconKind::Help
                />
            </NavSecondary>
            <NavFooter>
                <MinifyButton title=MaybeProp::derive(title) on:click=move |_| ctx.switch() />
            </NavFooter>
        </nav>
    }
}
