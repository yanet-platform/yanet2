import type React from 'react';

// Table dimensions
export const ROW_HEIGHT = 32;
export const HEADER_HEIGHT = 40;
export const SEARCH_BAR_HEIGHT = 52;
export const TOOLBAR_HEIGHT = 48;
export const FOOTER_HEIGHT = 28;
export const OVERSCAN = 20;

// Column widths for packet table (flexible layout)
export const COLUMN_WIDTHS = {
    index: 50,
    time: 100,
    source: 1, // flex
    destination: 1, // flex
    protocol: 70,
    length: 55,
    info: 2, // flex (wider)
} as const;

// Fixed columns total width (for min-width calculation)
export const FIXED_COLUMNS_WIDTH = 50 + 100 + 70 + 55 + 16; // index + time + protocol + length + padding

// Pre-computed cell styles
export const cellStyles: Record<keyof typeof COLUMN_WIDTHS, React.CSSProperties> = {
    index: {
        width: COLUMN_WIDTHS.index,
        minWidth: COLUMN_WIDTHS.index,
        maxWidth: COLUMN_WIDTHS.index,
        paddingRight: 8,
        textAlign: 'right',
        color: 'var(--g-color-text-secondary)',
        fontFamily: 'var(--g-font-family-monospace)',
        fontSize: 12,
        flexShrink: 0,
    },
    time: {
        width: COLUMN_WIDTHS.time,
        minWidth: COLUMN_WIDTHS.time,
        maxWidth: COLUMN_WIDTHS.time,
        paddingRight: 8,
        fontFamily: 'var(--g-font-family-monospace)',
        fontSize: 12,
        flexShrink: 0,
    },
    source: {
        flex: 1,
        minWidth: 120,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        fontFamily: 'var(--g-font-family-monospace)',
        fontSize: 12,
    },
    destination: {
        flex: 1,
        minWidth: 120,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        fontFamily: 'var(--g-font-family-monospace)',
        fontSize: 12,
    },
    protocol: {
        width: COLUMN_WIDTHS.protocol,
        minWidth: COLUMN_WIDTHS.protocol,
        maxWidth: COLUMN_WIDTHS.protocol,
        paddingRight: 8,
        fontFamily: 'var(--g-font-family-monospace)',
        fontSize: 12,
        flexShrink: 0,
    },
    length: {
        width: COLUMN_WIDTHS.length,
        minWidth: COLUMN_WIDTHS.length,
        maxWidth: COLUMN_WIDTHS.length,
        paddingRight: 8,
        textAlign: 'right',
        fontFamily: 'var(--g-font-family-monospace)',
        fontSize: 12,
        flexShrink: 0,
    },
    info: {
        flex: 2,
        minWidth: 150,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        color: 'var(--g-color-text-secondary)',
        fontFamily: 'var(--g-font-family-monospace)',
        fontSize: 12,
    },
};

// Minimum total width
export const TOTAL_WIDTH = 800;
