use std::borrow::Cow;

use leptos::prelude::*;

/// The input controlled component.
#[component]
pub fn Input<F>(
    /// Current value.
    #[prop(into)]
    value: Signal<String>,
    /// Called each time the input changes.
    on_input: F,
    /// The HTML input type.
    #[prop(default = "text")]
    ty: &'static str,
    /// Hint to the user as to what kind of information is expected in the
    /// field.
    #[prop(into, default = "".into())]
    placeholder: Cow<'static, str>,
    /// The HTML spellcheck attribute.
    #[prop(default = false)]
    spellcheck: bool,
    /// Whether this input is disabled, i.e. should not react to any external
    /// event.
    #[prop(optional, into)]
    is_disabled: Option<Signal<bool>>,
    /// Whether this field contains errors.
    ///
    /// By default color indication is applied.
    #[prop(optional, into)]
    is_error: Option<Signal<bool>>,
) -> impl IntoView
where
    F: Fn(String) + 'static,
{
    let is_disabled = move || is_disabled.map(|v| v.get()).unwrap_or_default();
    let is_error = move || is_error.map(|v| v.get()).unwrap_or_default();

    view! {
        <div class="noc-input" class:error=is_error class:disabled=is_disabled>
            <input
                type=ty
                placeholder=placeholder.to_string()
                spellcheck=spellcheck.to_string()
                disabled=is_disabled
                prop:value=move || value.get()
                on:input=move |ev| {
                    ev.prevent_default();
                    on_input(event_target_value(&ev));
                }
            />
        </div>
    }
}
