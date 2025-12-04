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
    nextHop: 320,
    peer: 320,
    isBest: 60,
    pref: 60,
    asPathLen: 80,
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
    nextHop: {
        width: COLUMN_WIDTHS.nextHop,
        minWidth: COLUMN_WIDTHS.nextHop,
        maxWidth: COLUMN_WIDTHS.nextHop,
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
    isBest: {
        width: COLUMN_WIDTHS.isBest,
        minWidth: COLUMN_WIDTHS.isBest,
        maxWidth: COLUMN_WIDTHS.isBest,
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
    asPathLen: {
        width: COLUMN_WIDTHS.asPathLen,
        minWidth: COLUMN_WIDTHS.asPathLen,
        maxWidth: COLUMN_WIDTHS.asPathLen,
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
