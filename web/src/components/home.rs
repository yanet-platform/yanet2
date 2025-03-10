use leptos::prelude::*;

use crate::components::common::{
    center::Center,
    header::Header,
    spinner::{Spinner, SpinnerSize},
    viewport::{Viewport, ViewportContent},
};

#[component]
pub fn Home() -> impl IntoView {
    view! {
        <Viewport>
            <Header>
                <h1>"Home"</h1>
            </Header>

            <ViewportContent>
                <Center>
                    <Spinner size=SpinnerSize::Large />
                </Center>
            </ViewportContent>
        </Viewport>
    }
}
