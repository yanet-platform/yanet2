use leptos::prelude::*;

use crate::components::common::{header::Header, tabs::{TabNav, TabNavLink}, viewport::{Viewport, ViewportContent}};

#[component]
pub fn RouteLookupView() -> impl IntoView {
    view! {
        <Viewport>
            <Header class="route-ph">
                <h1>"Route Lookup"</h1>
            </Header>

            <ViewportContent class="route-pc">
                <TabNav>
                    <TabNavLink name="RIB Lookup" href="/route/lookup" />
                    <TabNavLink name="RIB Show" href="/route/show" />
                    <TabNavLink name="Route Add" href="/route/add" />
                </TabNav>
            </ViewportContent>
        </Viewport>
    }
}