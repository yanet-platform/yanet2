use leptos::prelude::*;

use crate::components::common::spinner::{Spinner, SpinnerSize};

#[component]
pub fn Button(
    /// A basic button is less pronounced.
    #[prop(optional, into)]
    basic: MaybeProp<bool>,
    /// A button can be formatted to show different levels of emphasis.
    #[prop(optional, into)]
    primary: MaybeProp<bool>,
    /// Whether the button is disabled.
    #[prop(optional, into)]
    disabled: MaybeProp<bool>,
    /// Whether the loading icon should be shown.
    #[prop(optional, into)]
    loading: MaybeProp<bool>,
    /// Button color.
    #[prop(optional, into)]
    color: MaybeProp<ButtonColor>,
    /// Nested children.
    children: Children,
) -> impl IntoView {
    let class = move || {
        let mut class = "noc-button".to_string();
        if basic.get().unwrap_or_default() {
            class.push_str(" basic");
        }
        if primary.get().unwrap_or_default() {
            class.push_str(" primary");
        }
        if loading.get().unwrap_or_default() {
            class.push_str(" loading");
        }
        match color.get() {
            Some(ButtonColor::Red) => class.push_str(" red"),
            None => {}
        }

        class
    };

    view! {
        <button type="button" class=class disabled=move || disabled.get()>
            <span class="button-content">
                <Show when=move || loading.get().unwrap_or_default()>
                    <Spinner size=SpinnerSize::Small />
                </Show>

                {children()}
            </span>
        </button>
    }
}

#[derive(Debug, Clone, Copy)]
pub enum ButtonColor {
    Red,
}
