use tabled::{
    settings::{
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
        Color, Style,
    },
    Table, Tabled,
};

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
    println!("{table}");
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
