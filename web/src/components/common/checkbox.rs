use leptos::{ev::MouseEvent, prelude::*};

#[component]
pub fn CheckBox<C, F>(
    /// Options.
    #[prop(default = Vec::new())]
    options: Vec<&'static str>,
    /// Currently selected option's index.
    current: C,
    /// Called when the current selected option is required to be changed,
    /// i.e. on user interaction.
    on_changed: F,
) -> impl IntoView
where
    C: Fn() -> usize + Clone + Copy + 'static + Send,
    F: Fn(usize) + Clone + Copy + 'static + Send,
{
    let options = move || {
        options
            .iter()
            .enumerate()
            .map(|(idx, &text)| {
                let is_checked = idx == current();
                let aria_checked = if is_checked { "true" } else { "false" };
                let class = if is_checked {
                    "checkbox__option checked"
                } else {
                    "checkbox__option"
                };
                let value = text.to_lowercase();

                let on_click = move |ev: MouseEvent| {
                    ev.prevent_default();
                    if !is_checked {
                        on_changed(idx);
                    }
                };

                view! {
                    <label class=class>
                        <input
                            class="checkbox__option-control"
                            type="radio"
                            aria-checked=aria_checked
                            value=value
                            on:click=on_click
                        />
                        <span class="checkbox__option-text">{text}</span>
                    </label>
                }
            })
            .collect::<Vec<_>>()
    };

    view! {
        <div class="checkbox" role="radiogroup">
            {options}
        </div>
    }
}
