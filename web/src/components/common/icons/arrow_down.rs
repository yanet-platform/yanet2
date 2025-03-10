use leptos::prelude::*;

#[component]
pub fn IconArrowDown() -> impl IntoView {
    view! {
        <svg
            xmlns="http://www.w3.org/2000/svg"
            width="16"
            height="16"
            fill="currentColor"
            stroke="none"
            class="noc-icon"
        >
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16">
                <path stroke="currentColor" fill="none" d="M3 6l5 5 5-5"></path>
            </svg>
        </svg>
    }
}
