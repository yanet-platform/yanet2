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

/// Apply the standard YANET table style to `table`.
fn apply_style(table: &mut Table) {
    table.with(
        Style::modern()
            .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
            .remove_frame()
            .remove_horizontal(),
    );
    table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));
    table.modify(Rows::first(), Color::BOLD);
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
    use super::bar_len;

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
}
