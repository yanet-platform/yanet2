use tabled::{
    settings::{
        object::{Columns, Rows},
        peaker::Priority,
        style::{BorderColor, HorizontalLine},
        Color, Style, Width,
    },
    Table, Tabled,
};
use terminal_size::terminal_size_of;

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

/// Width in columns [`print_table`] would give `table` on an unbounded
/// terminal: the shared style applied, nothing wrapped.
///
/// Lets a renderer decide what to leave out before the table is fitted,
/// with the width semantics the renderer itself uses for wide characters
/// and colour escapes.
pub fn styled_width(table: &Table) -> usize {
    let mut table = table.clone();
    apply_style(&mut table);
    table.total_width()
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

/// Glyphs a histogram is drawn with, chosen once from
/// [`crate::output::is_colored`].
///
/// The whole set degrades together, so a non-UTF-8 locale or a piped run
/// never mixes block characters with ASCII. The sparkline has no ASCII
/// form at all: a row of punctuation reads worse than no shape, and the
/// bucket listing still carries it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Glyphs {
    /// Header of a bucket-bound column, read "at most".
    pub at_most: &'static str,
    /// Range separator inside a collapsed run of buckets.
    pub ellipsis: &'static str,
    bar_full: char,
    bar_partial: &'static [char],
    spark: Option<&'static [char]>,
}

impl Glyphs {
    /// Picks the Unicode or ASCII set from the shared colour decision.
    pub fn detect() -> Self {
        if crate::output::is_colored() {
            Self::unicode()
        } else {
            Self::ascii()
        }
    }

    /// Block characters, eighth-block bar remainders and a sparkline.
    pub const fn unicode() -> Self {
        Self {
            at_most: "≤",
            ellipsis: "…",
            bar_full: '█',
            bar_partial: &['▏', '▎', '▍', '▌', '▋', '▊', '▉'],
            spark: Some(&['▁', '▂', '▃', '▄', '▅', '▆', '▇']),
        }
    }

    /// Hash bars, whole cells only, no sparkline.
    pub const fn ascii() -> Self {
        Self {
            at_most: "<=",
            ellipsis: "...",
            bar_full: '#',
            bar_partial: &[],
            spark: None,
        }
    }

    /// Whether [`sparkline`] draws anything with this set.
    pub const fn has_sparkline(&self) -> bool {
        self.spark.is_some()
    }

    /// The glyph [`sparkline`] draws for an empty bucket, so a renderer can
    /// tone the baseline down and let the populated buckets stand out.
    pub fn spark_zero(&self) -> Option<char> {
        self.spark.and_then(|levels| levels.first().copied())
    }
}

/// Draws a bar of at most `width` cells proportional to `count / max`.
///
/// A non-zero count always shows at least a partial cell, so a populated
/// bucket next to a dominant one is never drawn as empty. Zero counts and
/// a zero maximum draw nothing.
pub fn bar(count: u64, max: u64, width: usize, glyphs: &Glyphs) -> String {
    if count == 0 || max == 0 || width == 0 {
        return String::new();
    }

    let cells = count as f64 / max as f64 * width as f64;
    let mut full = cells.floor() as usize;
    let eighths = ((cells - full as f64) * 8.0).round() as usize;

    if eighths == 8 {
        full += 1;
    }

    let mut bar: String = core::iter::repeat_n(glyphs.bar_full, full).collect();

    match glyphs.bar_partial.get(eighths.wrapping_sub(1)) {
        Some(partial) if eighths < 8 => bar.push(*partial),
        _ if bar.is_empty() => bar.push(*glyphs.bar_partial.first().unwrap_or(&glyphs.bar_full)),
        _ => {}
    }

    bar
}

/// Draws one character per bucket scaled to the largest count.
///
/// An empty bucket takes the lowest glyph and any populated bucket at
/// least the next one, so a bucket with one observation beside a bucket
/// with a billion still shows. The tallest glyph stops an eighth short of
/// the cell top, so the sparklines of stacked rows never touch and read
/// as one column each. Returns `None` when the glyph set has no
/// sparkline.
pub fn sparkline(counts: &[u64], glyphs: &Glyphs) -> Option<String> {
    let levels = glyphs.spark?;
    let max = counts.iter().copied().max().unwrap_or(0);
    let top = levels.len() - 1;

    let line = counts
        .iter()
        .map(|&count| {
            if count == 0 || max == 0 {
                return levels[0];
            }

            let level = 1 + (count as f64 / max as f64 * (top - 1) as f64).round() as usize;

            levels[level.clamp(1, top)]
        })
        .collect();

    Some(line)
}

#[cfg(test)]
mod test {
    use super::{bar, bar_len, sparkline, wrap_words, Glyphs};

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

    #[test]
    fn test_bar_full_width_at_the_maximum() {
        assert_eq!("████", bar(10, 10, 4, &Glyphs::unicode()));
        assert_eq!("####", bar(10, 10, 4, &Glyphs::ascii()));
    }

    #[test]
    fn test_bar_remainder_uses_eighth_blocks() {
        // 3 of 8 over 4 cells is 1.5 cells: one full block and a half.
        assert_eq!("█▌", bar(3, 8, 4, &Glyphs::unicode()));
        assert_eq!("#", bar(3, 8, 4, &Glyphs::ascii()));
    }

    #[test]
    fn test_bar_remainder_that_rounds_to_a_whole_cell_becomes_one() {
        // 15 of 16 over one cell is 0.9375 cells, which rounds to eight
        // eighths and so to one full block, never to a phantom partial.
        assert_eq!("█", bar(15, 16, 1, &Glyphs::unicode()));
    }

    #[test]
    fn test_bar_nonzero_count_is_never_empty() {
        assert_eq!("▏", bar(1, 1_000_000, 32, &Glyphs::unicode()));
        assert_eq!("#", bar(1, 1_000_000, 32, &Glyphs::ascii()));
    }

    #[test]
    fn test_bar_zero_count_draws_nothing() {
        assert_eq!("", bar(0, 10, 4, &Glyphs::unicode()));
        assert_eq!("", bar(5, 0, 4, &Glyphs::unicode()));
    }

    #[test]
    fn test_sparkline_scales_to_the_largest_count() {
        assert_eq!(Some("▁▂▅▇".to_owned()), sparkline(&[0, 1, 50, 100], &Glyphs::unicode()));
    }

    #[test]
    fn test_sparkline_nonzero_count_is_never_the_zero_glyph() {
        assert_eq!(
            Some("▇▂".to_owned()),
            sparkline(&[1_000_000_000, 1], &Glyphs::unicode())
        );
    }

    #[test]
    fn test_sparkline_all_empty_is_flat() {
        assert_eq!(Some("▁▁▁".to_owned()), sparkline(&[0, 0, 0], &Glyphs::unicode()));
    }

    #[test]
    fn test_sparkline_has_no_ascii_form() {
        assert_eq!(None, sparkline(&[1, 2, 3], &Glyphs::ascii()));
    }
}
