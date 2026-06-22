import type React from 'react';

/** Height of a single packet row in pixels. */
export const PKT_ROW_HEIGHT = 32;
/** Height of the column header bar in pixels. */
export const PKT_HEADER_HEIGHT = 40;
/** Height of the search/toolbar bar in pixels. */
export const PKT_SEARCH_BAR_HEIGHT = 52;
/** Height of the footer status bar in pixels. */
export const PKT_FOOTER_HEIGHT = 28;
/** Number of rows to render outside the visible window. */
export const PKT_OVERSCAN = 20;
/** Minimum total table width in pixels. */
export const PKT_TOTAL_WIDTH = 800;

/** Column widths for the shared packet table. */
export const PKT_COLUMN_WIDTHS = {
    index: 50,
    time: 100,
    source: 1,       // flex
    destination: 1,  // flex
    protocol: 70,
    length: 55,
    info: 2,         // flex (wider)
} as const;

/** Pre-computed cell styles shared across header and rows. */
export const pktCellStyles: Record<keyof typeof PKT_COLUMN_WIDTHS, React.CSSProperties> = {
    index: {
        width: PKT_COLUMN_WIDTHS.index,
        minWidth: PKT_COLUMN_WIDTHS.index,
        maxWidth: PKT_COLUMN_WIDTHS.index,
        paddingRight: 8,
        textAlign: 'right',
        color: 'var(--g-color-text-secondary)',
        fontFamily: 'var(--g-font-family-monospace)',
        fontSize: 12,
        flexShrink: 0,
    },
    time: {
        width: PKT_COLUMN_WIDTHS.time,
        minWidth: PKT_COLUMN_WIDTHS.time,
        maxWidth: PKT_COLUMN_WIDTHS.time,
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
        width: PKT_COLUMN_WIDTHS.protocol,
        minWidth: PKT_COLUMN_WIDTHS.protocol,
        maxWidth: PKT_COLUMN_WIDTHS.protocol,
        paddingRight: 8,
        fontFamily: 'var(--g-font-family-monospace)',
        fontSize: 12,
        flexShrink: 0,
    },
    length: {
        width: PKT_COLUMN_WIDTHS.length,
        minWidth: PKT_COLUMN_WIDTHS.length,
        maxWidth: PKT_COLUMN_WIDTHS.length,
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
