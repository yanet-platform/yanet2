use std::{borrow::Cow, time::Duration};

use leptos::prelude::*;

use crate::components::common::{
    button::Button,
    dropdown::Dropdown,
    header::Header,
    input::Input,
    popover::{Popover, PopoverTrigger},
    popup::SpanPopup,
    progress::ProgressBar,
    snackbar::{SnackbarContext, SnackbarData},
    viewport::{Viewport, ViewportContent},
};

#[component]
pub fn DemoView() -> impl IntoView {
    let (input1, set_input1) = signal("".to_string());
    let (input2, set_input2) = signal("".to_string());
    let (input3, set_input3) = signal("".to_string());
    let (input4, set_input4) = signal("I am disabled :(".to_string());
    let (input5, set_input5) = signal("".to_string());

    let (dropdown1, set_dropdown1) = signal(None);

    let (progress, set_progress) = signal(0.0);
    set_interval(move || set_progress.update(|p| *p += 0.01), Duration::from_millis(20));

    let snackbar = expect_context::<SnackbarContext>();

    view! {
        <Viewport>
            <Header>
                <h1>"Demo"</h1>
                <Button
                    primary=true
                    on:click=move |_| snackbar.push(SnackbarData::error("Test popup"))
                >
                    "Add popup"
                </Button>
            </Header>

            <ViewportContent>
                <div class="demo">
                    // Simple controlled <Input>.
                    <Input value=input1 on_input=move |s| set_input1.set(s) />
                    // With placeholder.
                    <Input
                        placeholder="Something..."
                        value=input2
                        on_input=move |s| set_input2.set(s)
                    />
                    // Error.
                    <Input
                        value=input3
                        on_input=move |s| set_input3.set(s)
                        is_error=Signal::derive(|| true)
                    />
                    // Disabled.
                    <Popover>
                        <PopoverTrigger slot>
                            <Input
                                value=input4
                                on_input=move |s| set_input4.set(s)
                                is_disabled=Signal::derive(|| true)
                            />
                        </PopoverTrigger>
                        "This is popover"
                    </Popover>
                    <SpanPopup message=Signal::derive(|| "Field is required".into())>
                        <Input
                            value=input5
                            on_input=move |s| set_input5.set(s)
                            is_error=Signal::derive(|| true)
                        />
                    </SpanPopup>

                    <Dropdown
                        current=dropdown1
                        options=Signal::derive(|| {
                            vec![
                                Cow::from("John"),
                                "Smith".into(),
                                "Ivan".into(),
                                "Evgeny".into(),
                                "Laser".into(),
                                "Cratos".into(),
                                "Jared".into(),
                                "Lucy".into(),
                                "Mack".into(),
                                "Knife".into(),
                                "Nox".into(),
                                "Diablo".into(),
                                "Baal".into(),
                            ]
                        })
                        on_change=move |idx| {
                            set_dropdown1.set(Some(idx));
                        }
                    />

                    <ProgressBar value=progress />
                </div>
            </ViewportContent>
        </Viewport>
    }
}
