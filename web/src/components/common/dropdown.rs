use std::borrow::Cow;

use leptos::prelude::*;

use crate::components::common::icons::{Icon, IconKind};

#[component]
pub fn Dropdown<F>(
    /// Currently selected option's index.
    #[prop(into)]
    current: Signal<Option<usize>>,
    /// List of available options to select.
    #[prop(into)]
    options: Signal<Vec<Cow<'static, str>>>,
    /// Whether this dropdown is configured with errors.
    ///
    /// For example, when it is required to have a current option selected.
    ///
    /// By default if `true` the color indication is applied.
    #[prop(optional, into)]
    is_error: Option<Signal<bool>>,
    /// Fake option displayed if there is no currently selected option.
    #[prop(default = "—".into())]
    placeholder: Cow<'static, str>,
    /// Called when the user attempts to change the value.
    ///
    /// This is called even if the current index equals to the selected one.
    on_change: F,
) -> impl IntoView
where
    F: Fn(usize) + Clone + Send + 'static,
{
    // Whether a menu is shown.
    let (is_expanded, set_is_expanded) = signal(false);

    let value = move || {
        current
            .get()
            .and_then(|idx| options.with(|options| options.get(idx).cloned()))
    };

    // Displayed text, either the current value or a placeholder.
    let text = move || value().unwrap_or(placeholder.clone());

    let menu = {
        let on_change = on_change.clone();
        move || {
            options
                .get()
                .into_iter()
                .enumerate()
                .map(|(idx, v)| {
                    let is_active = move || Some(idx) == current.get();
                    let on_change = on_change.clone();

                    view! {
                        <div
                            class="noc-dropdown__menu__item"
                            class:active=is_active
                            role="option"
                            aria-selected=move || is_active().to_string()
                            on:click=move |ev| {
                                ev.prevent_default();
                                on_change(idx);
                            }
                        >
                            <span class="noc-dropdown__menu__item__text">{v}</span>
                        </div>
                    }
                })
                .collect::<Vec<_>>()
        }
    };

    view! {
        <div
            class="noc-dropdown"
            role="listbox"
            aria-expanded=move || is_expanded.get().to_string()
            tabindex="0"
            on:click=move |ev| {
                ev.prevent_default();
                set_is_expanded.update(|v| *v = !*v)
            }
            on:blur=move |ev| {
                ev.prevent_default();
                set_is_expanded.set(false);
            }
        >
            <div class="noc-dropdown__value" class:empty=move || value().is_none()>
                <span class="noc-dropdown__value__text">{text}</span>

                <div class="noc-dropdown__value__icon">
                    <Icon kind=IconKind::ArrowDown />
                </div>
            </div>

            <div class="noc-dropdown__menu" class:visible=move || is_expanded.get()>
                {menu}
            </div>
        </div>
    }
}

// menu - separate component
// search
// multiple selection
// todo: docs%
