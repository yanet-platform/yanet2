use core::{
    fmt::{self, Display, Formatter},
    net::{AddrParseError, IpAddr},
    time::Duration,
};

use leptos::prelude::*;
use leptos_router::hooks::query_signal;
use leptos_struct_table::*;
use wasmtimer::std::{SystemTime, UNIX_EPOCH};

use crate::{
    api::neighbour::{code, NeighbourClient},
    components::common::{
        center::Center,
        header::Header,
        input::Input,
        snackbar::SnackbarContext,
        spinner::{Spinner, SpinnerSize},
        viewport::{Viewport, ViewportContent},
    },
    xxx::{accesskey::accesskey, fuzz::fuzz_match},
};

#[component]
pub fn NeighbourView() -> impl IntoView {
    // For now push errors in the snackbar.
    // TODO (issue #??): render error page instead.
    let snackbar = expect_context::<SnackbarContext>();

    // Use query parameters as a state.
    let (search, set_search) = query_signal::<String>("s");

    let neighbours = LocalResource::new(move || async move {
        let service = NeighbourClient::new();

        match service.list_neighbours().await {
            Ok(neighbours) => neighbours,
            Err(err) => {
                snackbar.error(err);
                Default::default()
            }
        }
    });

    let neighbours = move || -> Option<_> {
        let neighbours = neighbours
            .get()?
            .take()
            .into_iter()
            .map(NeighbourEntry::try_from)
            .collect::<Result<Vec<_>, _>>();

        // In case of IP address parse error just render empty table.
        // TODO (issue #??): render error page instead.
        let neighbours = neighbours.unwrap_or_default();

        Some(neighbours)
    };

    let neighbours = move || -> Option<Vec<NeighbourEntry>> {
        let search = search.get().unwrap_or_default();
        let neighbours = neighbours();

        if search.is_empty() {
            return neighbours;
        }

        neighbours.map(|neighbours| {
            neighbours
                .into_iter()
                .filter(|neigh| fuzz_match(&search, &neigh.next_hop.to_string()))
                .collect()
        })
    };

    let search_hotkey = accesskey('s').unwrap_or("s".to_string());
    let search_placeholder = format!("Type '{search_hotkey}' to filter (fuzzy mode)");

    view! {
        <Viewport>
            <Header class:neighbour-ph=true>
                <h1>"Neighbours"</h1>

                <Input
                    value=Signal::derive(move || search.get().unwrap_or_default())
                    on_input=move |s| set_search.set(if s.is_empty() { None } else { Some(s) })
                    accesskey="s"
                    placeholder=search_placeholder
                />
            </Header>

            <ViewportContent class:neighbour-pc=true>
                <Suspense fallback=move || {
                    view! {
                        <Center>
                            <Spinner size=SpinnerSize::Large />
                        </Center>
                    }
                }>
                    {move || {
                        neighbours()
                            .map(|neighbours| {
                                view! {
                                    <table class="noc-table">
                                        <TableContent
                                            rows=neighbours
                                            scroll_container="html"
                                            sorting_mode=SortingMode::SingleColumn
                                        />
                                    </table>
                                }
                            })
                    }}
                </Suspense>
            </ViewportContent>
        </Viewport>
    }
}

// TODO: docs.
#[derive(Clone, TableRow)]
#[table(impl_vec_data_provider, sortable)]
struct NeighbourEntry {
    next_hop: IpAddr,
    link_addr: String,
    hardware_addr: String,
    state: State,
    age: Age,
}

impl TryFrom<code::NeighbourEntry> for NeighbourEntry {
    type Error = AddrParseError;

    fn try_from(entry: code::NeighbourEntry) -> Result<Self, Self::Error> {
        let next_hop = entry.next_hop.parse()?;
        let age = Duration::from_secs(entry.updated_at as u64);

        let entry = Self {
            next_hop,
            link_addr: entry.link_addr,
            hardware_addr: entry.hardware_addr,
            state: State(entry.state),
            age: Age(age),
        };

        Ok(entry)
    }
}

// TODO: docs.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub struct State(pub i32);

impl Display for State {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let v = match self {
            Self(0x00) => "NONE",
            Self(0x01) => "INCOMPLETE",
            Self(0x02) => "REACHABLE",
            Self(0x04) => "STALE",
            Self(0x08) => "DELAY",
            Self(0x10) => "PROBE",
            Self(0x20) => "FAILED",
            Self(0x40) => "NOARP",
            Self(0x80) => "PERMANENT",
            Self(..) => "UNKNOWN",
        };

        write!(f, "{v}")
    }
}

impl CellValue for State {
    type RenderOptions = ();

    fn render_value(self, _options: Self::RenderOptions) -> impl IntoView {
        view! { <>{self.to_string()}</> }
    }
}

// TODO: docs.
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord)]
pub struct Age(pub Duration);

impl Display for Age {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let now = SystemTime::now();
        let duration = match self {
            Self(age) => {
                let timestamp = UNIX_EPOCH + *age;
                now.duration_since(timestamp).unwrap_or_default()
            }
        };

        write!(f, "{:.2?}", duration)
    }
}

impl CellValue for Age {
    type RenderOptions = ();

    fn render_value(self, _options: Self::RenderOptions) -> impl IntoView {
        view! { <>{self.to_string()}</> }
    }
}
