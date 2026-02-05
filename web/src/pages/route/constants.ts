import type React from 'react';

// Table dimensions
export const ROW_HEIGHT = 36;
export const HEADER_HEIGHT = 44;
export const SEARCH_BAR_HEIGHT = 52;
export const FOOTER_HEIGHT = 32;
export const OVERSCAN = 15;

// Column widths for virtualized table
export const COLUMN_WIDTHS = {
    checkbox: 40,
    index: 90,
    prefix: 320,
    next_hop: 320,
    peer: 320,
    is_best: 60,
    pref: 60,
    as_path_len: 80,
    source: 80,
} as const;

export const TOTAL_WIDTH = Object.values(COLUMN_WIDTHS).reduce((a, b) => a + b, 0);

// Route source labels
export const ROUTE_SOURCES = ['Unknown', 'Static', 'BIRD'] as const;

// Pre-computed cell styles for virtualized table
export const cellStyles: Record<keyof typeof COLUMN_WIDTHS, React.CSSProperties> = {
    checkbox: {
        width: COLUMN_WIDTHS.checkbox,
        minWidth: COLUMN_WIDTHS.checkbox,
        maxWidth: COLUMN_WIDTHS.checkbox,
        paddingRight: 8,
        display: 'flex',
        justifyContent: 'center',
    },
    index: {
        width: COLUMN_WIDTHS.index,
        minWidth: COLUMN_WIDTHS.index,
        maxWidth: COLUMN_WIDTHS.index,
        paddingRight: 8,
        textAlign: 'right',
        color: 'var(--g-color-text-secondary)',
    },
    prefix: {
        width: COLUMN_WIDTHS.prefix,
        minWidth: COLUMN_WIDTHS.prefix,
        maxWidth: COLUMN_WIDTHS.prefix,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        userSelect: 'text',
    },
    next_hop: {
        width: COLUMN_WIDTHS.next_hop,
        minWidth: COLUMN_WIDTHS.next_hop,
        maxWidth: COLUMN_WIDTHS.next_hop,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        userSelect: 'text',
    },
    peer: {
        width: COLUMN_WIDTHS.peer,
        minWidth: COLUMN_WIDTHS.peer,
        maxWidth: COLUMN_WIDTHS.peer,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        userSelect: 'text',
    },
    is_best: {
        width: COLUMN_WIDTHS.is_best,
        minWidth: COLUMN_WIDTHS.is_best,
        maxWidth: COLUMN_WIDTHS.is_best,
        paddingRight: 8,
        userSelect: 'text',
    },
    pref: {
        width: COLUMN_WIDTHS.pref,
        minWidth: COLUMN_WIDTHS.pref,
        maxWidth: COLUMN_WIDTHS.pref,
        paddingRight: 8,
        userSelect: 'text',
    },
    as_path_len: {
        width: COLUMN_WIDTHS.as_path_len,
        minWidth: COLUMN_WIDTHS.as_path_len,
        maxWidth: COLUMN_WIDTHS.as_path_len,
        paddingRight: 8,
        userSelect: 'text',
    },
    source: {
        width: COLUMN_WIDTHS.source,
        minWidth: COLUMN_WIDTHS.source,
        maxWidth: COLUMN_WIDTHS.source,
        paddingRight: 8,
        userSelect: 'text',
    },
};
