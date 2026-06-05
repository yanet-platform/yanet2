/** A command item displayed in the palette. */
export interface Command {
    /** Unique identifier for this command. */
    id: string;
    /** Single glyph or emoji displayed as the command icon. */
    icon: string;
    /** Human-readable label shown in the list. */
    label: string;
    /** Optional secondary line displayed below the label. */
    sub?: string;
    /** Extra text fed to fuzzy matching beyond label (not displayed). */
    keywords?: string;
    /** Optional group label; consecutive items with the same group are visually grouped. */
    group?: string;
    /** Called when the command is selected. */
    onSelect: () => void;
}

/** Adapts an array of domain rows into palette items. */
export interface RowAdapter<T> {
    /** Source rows to search over. */
    rows: T[];
    /** Extract a stable string id from a row. */
    getId: (row: T) => string;
    /** Extract the label text shown in the palette. */
    getLabel: (row: T) => string;
    /** Extract an optional secondary line shown below the label. */
    getSub?: (row: T) => string;
    /** Text fed to fuzzyMatch (may combine multiple fields). */
    searchText: (row: T) => string;
    /** Called with the row id when the user selects a row. */
    onSelect: (id: string) => void;
    /** Icon glyph for row items. Defaults to '→'. */
    icon?: string;
    /** Maximum number of row results to show. Defaults to 7. */
    max?: number;
}
