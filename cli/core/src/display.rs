use core::fmt::{Arguments, Display};

use tabled::{
    Table, Tabled,
    settings::{
        Color, Style, Width,
        object::{Columns, Rows},
        peaker::Priority,
        style::{BorderColor, HorizontalLine},
    },
};
use terminal_size::terminal_size_of;

use crate::output;

/// Prints `names` one per line, sorted and escaped, or reports an empty
/// list with `empty` through [`output::empty`].
///
/// Call it from the render closure of [`output::data`], like every other
/// human renderer.
pub fn print_names(names: &[String], empty: Arguments) {
    if names.is_empty() {
        output::empty(empty);
        return;
    }

    print_sorted(names);
}

/// [`print_names`] whose empty report also carries `hint`, see
/// [`output::empty_with_hint`].
pub fn print_names_with_hint(names: &[String], empty: Arguments, hint: Arguments) {
    if names.is_empty() {
        output::empty_with_hint(empty, hint);
        return;
    }

    print_sorted(names);
}

/// The key/value block of one object: keys dimmed and aligned, a list
/// value one item per line, every cell escaped like [`escape_wire_text`].
///
/// Print it from the render closure of [`output::data`], like every other
/// human renderer.
#[derive(Debug, Default, PartialEq, Eq)]
pub struct KeyValue {
    entries: Vec<(String, Vec<String>)>,
}

impl KeyValue {
    pub fn new() -> Self {
        Self::default()
    }

    /// Adds one value.
    pub fn row(mut self, key: &str, value: impl Display) -> Self {
        self.entries
            .push((escape_wire_text(key), vec![escape_wire_text(&value.to_string())]));

        self
    }

    /// Adds a list, one item per line, `-` when it is empty.
    pub fn rows<I>(mut self, key: &str, items: I) -> Self
    where
        I: IntoIterator,
        I::Item: Display,
    {
        let mut lines: Vec<String> = items
            .into_iter()
            .map(|item| escape_wire_text(&item.to_string()))
            .collect();

        if lines.is_empty() {
            lines.push("-".to_owned());
        }

        self.entries.push((escape_wire_text(key), lines));

        self
    }

    /// The entries in insertion order, keys and lines already escaped.
    pub fn entries(&self) -> &[(String, Vec<String>)] {
        &self.entries
    }

    pub fn print(&self) {
        for (label, text) in self.cells() {
            println!("{} {text}", output::dim(&label));
        }
    }

    fn cells(&self) -> Vec<(String, String)> {
        let width = self
            .entries
            .iter()
            .map(|(key, _)| key.chars().count() + 1)
            .max()
            .unwrap_or(0);
        let mut cells = Vec::new();

        for (key, lines) in &self.entries {
            let mut label = format!("{key}:");

            for line in lines {
                cells.push((format!("{label:<width$}"), line.clone()));
                label.clear();
            }
        }

        cells
    }
}

fn print_sorted(names: &[String]) {
    for name in sorted_names(names) {
        println!("{name}");
    }
}

fn sorted_names(names: &[String]) -> Vec<String> {
    let mut names: Vec<&str> = names.iter().map(String::as_str).collect();
    names.sort_unstable();

    names.into_iter().map(escape_wire_text).collect()
}

/// Spells out the non-printable characters, quotes and backslashes of text
/// that came off the wire, so two names that differ cannot read alike.
pub fn escape_wire_text(value: &str) -> String {
    value.escape_debug().to_string()
}

/// Print a table to stdout.
pub fn print_table_from_entries<I, T>(entries: I)
where
    I: IntoIterator<Item = T>,
    T: Tabled,
{
    let table = Table::new(entries);
    print_table(table);
}

pub fn print_table(mut table: Table) {
    apply_style(&mut table);
    fit_terminal_width(&mut table);
    println!("{table}");
}

/// Wrap the widest column(s) so the rendered table fits the current terminal
/// width.
///
/// Width is detected from stdout. When stdout is not a TTY (piped or
/// redirected) the width is unknown and the table is left unconstrained.
pub fn fit_terminal_width(table: &mut Table) {
    if let Some((terminal_size::Width(cols), _)) = terminal_size_of(std::io::stdout()) {
        table.with(
            Width::wrap(cols as usize)
                .priority(Priority::max(false))
                .keep_words(true),
        );
    }
}

/// Returns the current terminal width in columns, detected from stdout.
///
/// Returns `None` when stdout is not a TTY (piped or redirected), matching
/// the same detection [`fit_terminal_width`] uses. See [`stderr_width`] for
/// the stderr counterpart.
pub fn terminal_width() -> Option<usize> {
    terminal_size_of(std::io::stdout()).map(|(terminal_size::Width(cols), _)| cols as usize)
}

/// Returns the current terminal width in columns, detected from stderr.
///
/// Counterpart to [`terminal_width`], which reads stdout.
/// [`crate::output::empty`] and [`crate::output::empty_with_hint`] write their
/// report to stderr, so their wrapping must be measured against that channel
/// instead.
///
/// Stdout redirected is not a reachable case here: both callers already
/// return early whenever stdout itself is not a terminal. The case this
/// function exists for is the opposite one — stderr redirected to a file
/// while stdout stays a terminal. Measuring stdout there would wrap the
/// file stderr is writing to against an unrelated terminal's width;
/// [`crate::output::is_colored`], which already reads stderr for the same
/// reason, is the existing precedent for measuring this channel instead.
pub fn stderr_width() -> Option<usize> {
    terminal_size_of(std::io::stderr()).map(|(terminal_size::Width(cols), _)| cols as usize)
}

/// Greedily wraps `text` into lines of at most `width` columns, breaking
/// only at whitespace.
///
/// A word longer than `width` is kept whole on its own (overflowing) line
/// rather than split. Returns `text` unchanged as a single line when
/// `width` is `0`.
pub fn wrap_words(text: &str, width: usize) -> Vec<String> {
    if width == 0 {
        return vec![text.to_string()];
    }

    let mut lines = Vec::new();
    let mut current = String::new();

    for word in text.split_whitespace() {
        if current.is_empty() {
            current.push_str(word);
        } else if current.chars().count() + 1 + word.chars().count() <= width {
            current.push(' ');
            current.push_str(word);
        } else {
            lines.push(core::mem::take(&mut current));
            current.push_str(word);
        }
    }

    if !current.is_empty() || lines.is_empty() {
        lines.push(current);
    }

    lines
}

/// Apply the standard YANET table style to `table`.
///
/// Border and header color follow [`crate::output::is_colored`], so a
/// `NO_COLOR` run, a non-UTF-8 locale, or a redirected stderr gets the same
/// border layout with no ANSI escapes. The border glyph set itself is
/// unconditional.
fn apply_style(table: &mut Table) {
    /// Colour of a table's cell borders.
    const TABLE_BORDER_COLOR: (u8, u8, u8) = (0x4e, 0x4e, 0x4e);

    table.with(
        Style::modern()
            .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
            .remove_frame()
            .remove_horizontal(),
    );

    if crate::output::is_colored() {
        let (r, g, b) = TABLE_BORDER_COLOR;
        table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(r, g, b)));
        table.modify(Rows::first(), Color::BOLD);
    }
}

/// Returns the bar length for a histogram bucket, scaled to `BAR_MAX`.
///
/// Returns `0` when `max_count` is `0`. Non-zero counts that round to `0`
/// are bumped to `1` so every populated bucket shows at least one bar
/// character.
pub fn bar_len(count: u64, max_count: u64) -> usize {
    const BAR_MAX: usize = 20;

    if max_count == 0 {
        return 0;
    }

    let mut n = ((count as f64 / max_count as f64) * BAR_MAX as f64).round() as usize;

    if count > 0 && n == 0 {
        n = 1;
    }

    n
}

#[cfg(test)]
mod test {
    use super::{KeyValue, bar_len, escape_wire_text, sorted_names, wrap_words};

    #[test]
    fn test_key_value_escapes_every_cell_and_marks_an_empty_list() {
        let block = KeyValue::new()
            .row("name", "fw\nstate0")
            .rows("prefixes", ["10.0.0.0/8", "10.1.0.0/16"])
            .rows("mappings", Vec::<String>::new())
            .row("na\u{1b}me", "x");

        assert_eq!(
            &[
                ("name".to_owned(), vec!["fw\\nstate0".to_owned()]),
                (
                    "prefixes".to_owned(),
                    vec!["10.0.0.0/8".to_owned(), "10.1.0.0/16".to_owned()]
                ),
                ("mappings".to_owned(), vec!["-".to_owned()]),
                ("na\\u{1b}me".to_owned(), vec!["x".to_owned()]),
            ],
            block.entries()
        );
    }

    #[test]
    fn test_key_value_cells_pad_labels_and_continue_lists() {
        let block = KeyValue::new()
            .row("name", "fw\u{1b}0")
            .rows("prefixes", ["10.0.0.0/8", "10.1.0.0/16"]);

        assert_eq!(
            vec![
                ("name:    ".to_owned(), "fw\\u{1b}0".to_owned()),
                ("prefixes:".to_owned(), "10.0.0.0/8".to_owned()),
                ("         ".to_owned(), "10.1.0.0/16".to_owned()),
            ],
            block.cells()
        );
    }

    #[test]
    fn test_escape_wire_text_spells_out_control_and_invisible_characters() {
        assert_eq!("decap0", escape_wire_text("decap0"));
        assert_eq!("a\\nb", escape_wire_text("a\nb"));
        assert_eq!("\\u{1b}\\r[2J", escape_wire_text("\u{1b}\r[2J"));
        assert_eq!("a\\u{200b}b", escape_wire_text("a\u{200b}b"));
    }

    #[test]
    fn test_escape_wire_text_keeps_distinct_names_distinct() {
        assert_ne!(escape_wire_text("a\\nb"), escape_wire_text("a\nb"));
    }

    #[test]
    fn test_sorted_names_sorts_the_raw_names_before_escaping() {
        let names = ["de\ncap".to_owned(), "a\u{7f}".to_owned(), "a}".to_owned()];

        assert_eq!(vec!["a}", "a\\u{7f}", "de\\ncap"], sorted_names(&names));
    }

    #[test]
    fn bar_len_scaling() {
        assert_eq!(20, bar_len(310, 310));
        assert_eq!(3, bar_len(45, 310));
        assert_eq!(0, bar_len(0, 310));
    }

    #[test]
    fn bar_len_edge_cases() {
        assert_eq!(1, bar_len(1, 1000));
        assert_eq!(0, bar_len(5, 0));
    }

    #[test]
    fn wrap_words_fits_within_width() {
        assert_eq!(vec!["aa bb".to_string(), "cc".to_string()], wrap_words("aa bb cc", 5));
    }

    #[test]
    fn wrap_words_keeps_long_word_whole() {
        assert_eq!(
            vec!["superlongword".to_string(), "short".to_string()],
            wrap_words("superlongword short", 5)
        );
    }

    #[test]
    fn wrap_words_zero_width_returns_single_line() {
        assert_eq!(vec!["one long line".to_string()], wrap_words("one long line", 0));
    }

    #[test]
    fn wrap_words_counts_columns_not_bytes() {
        // Each em dash is 3 UTF-8 bytes but a single display column, so a
        // width of 5 must fit both of them plus their surrounding letters
        // on one line — byte-counting would wrap one column early.
        assert_eq!(vec!["a — b".to_string()], wrap_words("a — b", 5));
    }

    #[test]
    fn wrap_words_empty_text_returns_one_empty_line() {
        assert_eq!(vec![String::new()], wrap_words("", 10));
    }
}
