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
    prefix: 400,
} as const;

export const TOTAL_WIDTH = Object.values(COLUMN_WIDTHS).reduce((a, b) => a + b, 0);

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
        flex: 1,
    },
};

