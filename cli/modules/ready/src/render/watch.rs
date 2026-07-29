//! `--watch` transition tracking: the merged snapshot diff, the append-only
//! transition log line renderer, the transport-lifecycle line renderer used
//! by aggregate watch's reconnect-with-backoff supervisors, and the
//! registry membership line renderer used by its re-discovery sweep.

use core::time::Duration;
use std::collections::BTreeMap;

use chrono::Local;
use colored::Colorize;
use readinesspb::pb::{Scope, State};
use ync::{display, output};

use super::{StateStyle, Symbols, dim, layout::normalize_whitespace};

/// Gap between the transition line's prefix and the reason text that follows
/// it on the same line.
const REASON_GAP: usize = 3;

/// Trailing tag appended to a [`Transition::FirstSeen`] line, in the same
/// [`dim`] grey as reason text rather than the `stale` tag's amber — a first
/// sighting is informational, not a warning about aging data.
const FIRST_SEEN_TAG: &str = "first seen";

/// The kind of change observed for one scope in a `--watch` delta message.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Transition {
    /// No entry existed for this scope in the snapshot map — its very first
    /// observation, whatever its state.
    ///
    /// Deliberately distinct from `StateChanged { previous: State::Unknown
    /// }`: that would claim the scope was once actually seen as `UNKNOWN`,
    /// which never happened. A service picked up by the re-discovery sweep,
    /// or one whose initial probe failed, both surface here.
    FirstSeen,
    /// The scope's state changed away from `previous`.
    StateChanged { previous: State },
    /// The state is unchanged; only the reason changed.
    ReasonChanged,
    /// Neither the state nor the reason changed — only a heartbeat field
    /// (e.g. `observed_at`) advanced.
    Unchanged,
}

/// Records `scope`'s new data in `snapshot` and classifies the change.
///
/// `snapshot` is keyed by `(service, scope name)`, not scope name alone —
/// scope names collide across services (several operators share a
/// `reconcile` scope). A scope absent from `snapshot` returns
/// [`Transition::FirstSeen`] rather than being diffed against a synthesized
/// `STATE_UNKNOWN` entry — doing the latter would silently swallow a
/// genuinely first-seen `UNKNOWN`, reason-less scope, since it would then
/// compare equal to its own fabricated "previous" and render as nothing at
/// all. `snapshot` is updated with `scope`'s full data so later calls can
/// diff against it.
///
/// Only `state` and `reasons` are compared: `observed_at` and
/// `last_transition_time` advance on every heartbeat and must never by
/// themselves classify a message as changed — this mirrors the server's
/// own change predicate, which never emits a `Watch` message for a pure
/// heartbeat in the first place.
pub fn record_transition(snapshot: &mut BTreeMap<(String, String), Scope>, service: &str, scope: &Scope) -> Transition {
    let key = (service.to_owned(), scope.name.clone());
    let previous = snapshot.insert(key, scope.clone());

    let Some(previous) = previous else {
        return Transition::FirstSeen;
    };

    let previous_state = State::try_from(previous.state).unwrap_or_default();
    let next_state = State::try_from(scope.state).unwrap_or_default();

    if previous_state != next_state {
        return Transition::StateChanged { previous: previous_state };
    }

    if previous.reasons.as_slice() != scope.reasons.as_slice() {
        Transition::ReasonChanged
    } else {
        Transition::Unchanged
    }
}

/// The optional service cell prefixing a `--watch` transition line.
///
/// [`ServiceColumn::None`] is single-service watch, where every line names
/// the same service and repeating it would be noise. [`ServiceColumn::Named`]
/// is aggregate watch: `alias` is padded to `width`, which is measured at
/// startup and only ever widened as new services or scopes appear, never
/// shrunk.
#[derive(Debug, Clone, Copy)]
pub enum ServiceColumn<'a> {
    None,
    Named { alias: &'a str, width: usize },
}

impl ServiceColumn<'_> {
    /// Renders the cell text: the padded alias plus its separating space,
    /// or an empty string for [`ServiceColumn::None`].
    fn cell(self) -> String {
        match self {
            Self::None => String::new(),
            Self::Named { alias, width } => format!("{alias:<width$} "),
        }
    }
}

/// The transition line's prefix in both of its forms.
///
/// `plain` carries no ANSI escapes and is the only form ever measured;
/// `styled` is the only form ever printed. Measuring `styled` would count
/// the escape bytes as columns and blow up the hanging indent.
struct Prefix {
    plain: String,
    styled: String,
}

/// Builds the `HH:MM:SS  ✗ [service] name  PREV → NEXT` prefix of a
/// transition line.
///
/// `name` must already be padded to the scope-name column width. Every part
/// is rendered twice from the same text: once bare for [`Prefix::plain`] and
/// once through [`StateStyle`] for [`Prefix::styled`], so the two can never
/// disagree on the visible width. The mark and both state labels are colored
/// by their own state — the previous label keeps its old state's color — and
/// the timestamp, service cell, and arrow stay grey or uncolored.
/// [`Transition::FirstSeen`] renders the same single-label shape as a
/// reason-only change; [`print_transition_line`] appends the tag that tells
/// the two apart.
fn transition_prefix(
    timestamp: &str,
    service: ServiceColumn,
    name: &str,
    state: State,
    transition: Transition,
    colored: bool,
) -> Prefix {
    let symbols = Symbols::new(colored);
    let style = StateStyle::new(state, colored);
    let label = super::label(state);
    let service = service.cell();

    let (change, styled_change) = match transition {
        Transition::StateChanged { previous } => {
            let previous_style = StateStyle::new(previous, colored);
            let previous_label = super::label(previous);

            (
                format!("{previous_label} {} {label}", symbols.arrow),
                format!(
                    "{} {} {}",
                    previous_style.paint(previous_label),
                    dim(symbols.arrow, colored),
                    style.paint(label)
                ),
            )
        }
        Transition::ReasonChanged | Transition::Unchanged | Transition::FirstSeen => {
            // Padded to `STATE_WIDTH` so the trailing `first seen` tag and
            // any inline reason line up at a fixed column across every
            // single-label line — a `StateChanged`'s `PREV -> NEXT` shape
            // cannot share that column anyway, so it is left unpadded.
            let label = format!("{label:<width$}", width = super::STATE_WIDTH);

            (label.clone(), style.paint(&label))
        }
    };

    Prefix {
        plain: format!("{timestamp}  {} {service}{name} {change}", style.mark),
        styled: format!(
            "{}  {} {service}{name} {styled_change}",
            dim(timestamp, colored),
            style.styled_mark()
        ),
    }
}

/// Prints one append-only log line for a `--watch` change.
///
/// A state change shows `PREV → NEXT` on the transition line; a reason-only
/// change shows the unchanged state instead. [`Transition::FirstSeen`] shows
/// the unchanged-looking state too, but follows it with a trailing dim
/// `first seen` tag — otherwise a scope's very first sighting would be
/// indistinguishable from a reason-only change that happens to land on the
/// same state. Every kind then renders every `CODE: message` reason (if
/// any) on a wrapped, dim continuation line — the server never resends an
/// unchanged reason, so a persistent failure's message would otherwise
/// only ever be shown once. Long reason text wraps with a hanging indent
/// when stdout is a TTY; a scope with no reasons prints just the
/// transition line. `service` names the service the change belongs to in
/// aggregate watch, and is omitted in single-service watch — see
/// [`ServiceColumn`]. A caller must not pass [`Transition::Unchanged`]: the
/// whole point of that variant is that nothing is worth printing.
pub fn print_transition_line(service: ServiceColumn, scope: &Scope, name_width: usize, transition: Transition) {
    let colored = output::is_colored();
    let wrap_width = display::terminal_width();
    let timestamp = Local::now().format("%H:%M:%S").to_string();
    let state = State::try_from(scope.state).unwrap_or_default();
    let name = format!("{:<width$}", scope.name, width = name_width);

    let prefix = transition_prefix(&timestamp, service, &name, state, transition, colored);
    let first_seen_tag = matches!(transition, Transition::FirstSeen).then(|| dim(FIRST_SEEN_TAG, colored));
    let tag_width = first_seen_tag
        .as_ref()
        .map_or(0, |_| REASON_GAP + FIRST_SEEN_TAG.chars().count());
    let indent = prefix.plain.chars().count() + tag_width + REASON_GAP;

    print!("{}", prefix.styled);

    if let Some(tag) = &first_seen_tag {
        print!("{:width$}{tag}", "", width = REASON_GAP);
    }

    let mut is_first_line = true;
    for reason in &scope.reasons {
        let reason_text = super::format_reason(reason);

        let lines = match wrap_width {
            Some(width) if width > indent => display::wrap_words(&reason_text, width - indent),
            _ => vec![normalize_whitespace(&reason_text)],
        };

        for line in &lines {
            let styled = dim(line, colored);

            if is_first_line {
                print!("{:width$}{styled}", "", width = REASON_GAP);
                is_first_line = false;
            } else {
                print!("\n{:indent$}{styled}", "");
            }
        }
    }

    println!();
}

/// Prints one dim lifecycle line for `--watch` reconnect chatter that is
/// neither a readiness change nor a registry membership change, given an
/// already-composed `message`.
///
/// Callers compose `message` themselves — [`print_lost_line`] shows how a
/// lost stream's cause and retry delay become one such message.
/// `HH:MM:SS  [!] alias message`, the whole line dim — this is transport
/// chatter, not a readiness state, and must never borrow a [`StateStyle`]
/// colour or [`print_membership_line`]'s colored mark. Long messages wrap
/// with a hanging indent, the same way a transition line's reason text
/// does.
pub fn print_lifecycle_line(alias: &str, alias_width: usize, message: &str) {
    let colored = output::is_colored();
    let wrap_width = display::terminal_width();
    let timestamp = Local::now().format("%H:%M:%S").to_string();
    let mark = if colored { "[!]" } else { "[--]" };
    let name = format!("{alias:<alias_width$}");

    let prefix = format!("{timestamp}  {mark} {name} ");
    let indent = prefix.chars().count();

    print!("{}", dim(&prefix, colored));

    let lines = match wrap_width {
        Some(width) if width > indent => display::wrap_words(message, width - indent),
        _ => vec![normalize_whitespace(message)],
    };

    for (idx, line) in lines.iter().enumerate() {
        if idx > 0 {
            print!("\n{:indent$}", "");
        }
        print!("{}", dim(line, colored));
    }

    println!();
}

/// Prints one lifecycle line for a `--watch` supervisor losing its stream,
/// composing the raw disconnect `cause` and the backoff `retry_after` into
/// one human line.
///
/// `stream lost: {cause}{dash}reconnecting in {delay}`, `dash` taken from
/// [`Symbols`] so it degrades to ASCII off [`output::is_colored`] exactly
/// like every other glyph in this render. The composition happens here, at
/// render time, rather than where the event is built, so the
/// machine-readable payload's `error` field carries `cause` alone.
pub fn print_lost_line(alias: &str, alias_width: usize, cause: &str, retry_after: Duration) {
    let colored = output::is_colored();
    let symbols = Symbols::new(colored);
    let delay = humantime::format_duration(retry_after);
    let message = format!("stream lost: {cause}{}reconnecting in {delay}", symbols.dash);

    print_lifecycle_line(alias, alias_width, &message);
}

/// One direction of the single re-discovery-sweep delta the render loop
/// reports: a service the sweep found newly registered, or one it found
/// missing.
///
/// [`print_membership_line`] is parameterised by this enum precisely so a
/// service joining and a service departing render through one shared code
/// path rather than two independently maintained ones.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Membership {
    /// The sweep found a service that was not previously known.
    Discovered,
    /// The sweep found a previously known service now missing.
    Gone,
}

impl Membership {
    /// This direction's line message.
    fn message(self) -> &'static str {
        match self {
            Self::Discovered => "discovered in the gateway registry",
            Self::Gone => "gone from the gateway registry",
        }
    }
}

/// [`Membership`]'s mark and color, resolved once per call from the same
/// `colored` gate every other glyph in this render uses.
///
/// Mirrors [`StateStyle`]'s shape: the glyph itself changes between the
/// Unicode and ASCII forms, not just its color, chosen so neither
/// direction is ever confused with a readiness-state mark or with
/// [`print_lifecycle_line`]'s own `[!]` / `[--]` transport marks.
struct MembershipStyle {
    mark: &'static str,
    color: fn(&str) -> String,
    colored: bool,
}

impl MembershipStyle {
    fn new(membership: Membership, colored: bool) -> Self {
        let (unicode_mark, ascii_mark, color): (&str, &str, fn(&str) -> String) = match membership {
            Membership::Discovered => ("[▲]", "[^^]", |s| s.green().to_string()),
            Membership::Gone => ("[▼]", "[vv]", |s| s.red().to_string()),
        };

        let mark = if colored { unicode_mark } else { ascii_mark };

        Self { mark, color, colored }
    }

    /// Returns the mark glyph in this direction's color.
    fn styled_mark(&self) -> String {
        if self.colored {
            (self.color)(self.mark)
        } else {
            self.mark.to_string()
        }
    }
}

/// Prints one `--watch` registry membership change line: a service joining
/// or leaving the gateway's registry.
///
/// Structured like [`print_lifecycle_line`] — timestamp, bracketed mark,
/// alias padded to `alias_width`, wrapped message with a hanging indent —
/// but the mark keeps `membership`'s own color instead of [`dim`]'s grey: a
/// backend joining or leaving the registry is a state-level event, not
/// transport chatter, so the mark stays as prominent as a readiness
/// [`StateStyle`] mark. The alias prints at full weight and only the
/// descriptive message is dimmed, the same split a transition line makes
/// between its state labels and its dim reason text. `membership` alone
/// selects the mark, its color, and the message text, so the joined and
/// departed lines can never drift apart from one another.
pub fn print_membership_line(alias: &str, alias_width: usize, membership: Membership) {
    let colored = output::is_colored();
    let wrap_width = display::terminal_width();
    let timestamp = Local::now().format("%H:%M:%S").to_string();
    let style = MembershipStyle::new(membership, colored);
    let message = membership.message();
    let name = format!("{alias:<alias_width$}");

    let plain_prefix = format!("{timestamp}  {} {name} ", style.mark);
    let indent = plain_prefix.chars().count();

    print!("{}  {} {name} ", dim(&timestamp, colored), style.styled_mark());

    let lines = match wrap_width {
        Some(width) if width > indent => display::wrap_words(message, width - indent),
        _ => vec![normalize_whitespace(message)],
    };

    for (idx, line) in lines.iter().enumerate() {
        if idx > 0 {
            print!("\n{:indent$}", "");
        }
        print!("{}", dim(line, colored));
    }

    println!();
}

#[cfg(test)]
mod test {
    use readinesspb::pb::Reason;

    use super::*;

    fn scope(name: &str, state: State) -> Scope {
        Scope {
            name: name.to_string(),
            state: state as i32,
            reasons: Vec::new(),
            observed_at: None,
            last_transition_time: None,
        }
    }

    fn scope_with_reason(name: &str, state: State, code: &str) -> Scope {
        Scope {
            reasons: vec![Reason {
                code: code.to_owned(),
                message: String::new(),
            }],
            ..scope(name, state)
        }
    }

    /// Drops every ANSI SGR escape (`ESC [ … m`) from `text`, leaving the
    /// characters a terminal would actually show.
    fn strip_ansi(text: &str) -> String {
        let mut visible = String::new();
        let mut chars = text.chars();

        while let Some(c) = chars.next() {
            if c != '\x1b' {
                visible.push(c);
                continue;
            }

            for escaped in chars.by_ref() {
                if escaped == 'm' {
                    break;
                }
            }
        }

        visible
    }

    #[test]
    fn transition_prefix_colors_do_not_change_the_measured_width() {
        colored::control::set_override(true);

        let prefix = transition_prefix(
            "14:32:11",
            ServiceColumn::None,
            "rib         ",
            State::NotReady,
            Transition::StateChanged { previous: State::Ready },
            true,
        );

        assert_eq!(prefix.plain, strip_ansi(&prefix.styled));
        assert!(!prefix.plain.contains('\x1b'));
        assert!(prefix.styled.contains('\x1b'));
    }

    #[test]
    fn transition_prefix_uncolored_is_ascii_and_escape_free() {
        let prefix = transition_prefix(
            "14:32:11",
            ServiceColumn::None,
            "rib         ",
            State::NotReady,
            Transition::StateChanged { previous: State::Ready },
            false,
        );

        assert_eq!(prefix.plain, prefix.styled);
        assert!(prefix.plain.is_ascii());
        assert!(prefix.plain.contains("[xx]"));
        assert!(prefix.plain.contains("->"));
    }

    #[test]
    fn transition_prefix_reason_only_change_keeps_one_label() {
        colored::control::set_override(true);

        let prefix = transition_prefix(
            "14:32:11",
            ServiceColumn::None,
            "rib         ",
            State::Degraded,
            Transition::ReasonChanged,
            true,
        );

        assert_eq!(prefix.plain, strip_ansi(&prefix.styled));
        assert!(prefix.plain.contains(StateStyle::new(State::Degraded, true).mark));
        assert!(prefix.plain.contains("rib"));
        assert!(!prefix.plain.contains(Symbols::new(true).arrow));
    }

    #[test]
    fn transition_prefix_named_service_adds_a_padded_cell() {
        let with_service = transition_prefix(
            "14:32:11",
            ServiceColumn::Named { alias: "route", width: 10 },
            "rib         ",
            State::Ready,
            Transition::ReasonChanged,
            false,
        );
        let without_service = transition_prefix(
            "14:32:11",
            ServiceColumn::None,
            "rib         ",
            State::Ready,
            Transition::ReasonChanged,
            false,
        );

        assert!(with_service.plain.contains("route"));
        assert_eq!(
            without_service.plain.chars().count() + 11,
            with_service.plain.chars().count()
        );
    }

    #[test]
    fn membership_style_uncolored_mark_is_ascii_for_both_directions() {
        for membership in [Membership::Discovered, Membership::Gone] {
            let style = MembershipStyle::new(membership, false);

            assert!(style.mark.is_ascii());
            assert_eq!(style.mark, style.styled_mark());
            assert!(membership.message().is_ascii());
        }
    }

    #[test]
    fn record_transition_detects_state_change() {
        let mut snapshot = BTreeMap::new();
        snapshot.insert(("route".to_owned(), "rib".to_owned()), scope("rib", State::Ready));

        let transition = record_transition(&mut snapshot, "route", &scope("rib", State::Degraded));

        assert_eq!(Transition::StateChanged { previous: State::Ready }, transition);
    }

    #[test]
    fn record_transition_detects_reason_only_change() {
        let mut snapshot = BTreeMap::new();
        snapshot.insert(
            ("route".to_owned(), "rib".to_owned()),
            scope_with_reason("rib", State::Degraded, "BIRD_DOWN"),
        );

        let transition = record_transition(
            &mut snapshot,
            "route",
            &scope_with_reason("rib", State::Degraded, "TIMEOUT"),
        );

        assert_eq!(Transition::ReasonChanged, transition);
    }

    #[test]
    fn record_transition_is_unchanged_when_state_and_reasons_match() {
        let mut snapshot = BTreeMap::new();
        let mut previous = scope_with_reason("rib", State::Degraded, "BIRD_DOWN");
        previous.observed_at = Some(prost_types::Timestamp { seconds: 1, nanos: 0 });
        snapshot.insert(("route".to_owned(), "rib".to_owned()), previous);

        // Same state and reason, but `observed_at` advanced — a pure
        // heartbeat must not be classified as a change.
        let mut next = scope_with_reason("rib", State::Degraded, "BIRD_DOWN");
        next.observed_at = Some(prost_types::Timestamp { seconds: 2, nanos: 0 });

        let transition = record_transition(&mut snapshot, "route", &next);

        assert_eq!(Transition::Unchanged, transition);
    }

    #[test]
    fn record_transition_first_sighting_is_first_seen_regardless_of_state() {
        let mut snapshot = BTreeMap::new();

        let transition = record_transition(&mut snapshot, "route", &scope("neighbours", State::NotReady));

        assert_eq!(Transition::FirstSeen, transition);
        assert!(snapshot.contains_key(&("route".to_owned(), "neighbours".to_owned())));
    }

    #[test]
    fn record_transition_first_sighting_of_unknown_reasonless_scope_is_not_unchanged() {
        // This is the exact bug: synthesizing a fake `STATE_UNKNOWN`,
        // reason-less "previous" for an absent snapshot entry made a
        // genuinely first-seen `UNKNOWN` scope compare equal to it and
        // vanish as `Unchanged`.
        let mut snapshot = BTreeMap::new();

        let transition = record_transition(&mut snapshot, "route", &scope("neighbours", State::Unknown));

        assert_eq!(Transition::FirstSeen, transition);
        assert_ne!(Transition::Unchanged, transition);
    }

    #[test]
    fn record_transition_keys_by_service_and_scope_name() {
        let mut snapshot = BTreeMap::new();
        snapshot.insert(
            ("route".to_owned(), "reconcile".to_owned()),
            scope("reconcile", State::Ready),
        );

        // A same-named scope from a different service must not be diffed
        // against `route`'s entry — it must be a first sighting, not a
        // comparison against `route`'s `Ready` entry.
        let transition = record_transition(&mut snapshot, "decap", &scope("reconcile", State::NotReady));

        assert_eq!(Transition::FirstSeen, transition);
    }

    #[test]
    fn record_transition_updates_snapshot() {
        let mut snapshot = BTreeMap::new();
        snapshot.insert(("route".to_owned(), "rib".to_owned()), scope("rib", State::Ready));

        record_transition(&mut snapshot, "route", &scope("rib", State::NotReady));

        assert_eq!(
            State::NotReady as i32,
            snapshot[&("route".to_owned(), "rib".to_owned())].state
        );
    }
}
