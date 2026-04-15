use tabled::{
    settings::{
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
        Color, Style,
    },
    Table, Tabled,
};

pub fn apply_style(table: &mut Table) {
    table.with(
        Style::modern()
            .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
            .remove_frame()
            .remove_horizontal(),
    );
    table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));
    table.modify(Rows::first(), Color::BOLD);
}

pub fn print_table<I, T>(entries: I)
where
    I: IntoIterator<Item = T>,
    T: Tabled,
{
    let mut table = Table::new(entries);
    apply_style(&mut table);
    println!("{table}");
}
