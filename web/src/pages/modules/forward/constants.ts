import type React from 'react';

// Table dimensions
export const ROW_HEIGHT = 40;
export const HEADER_HEIGHT = 44;
export const SEARCH_BAR_HEIGHT = 52;
export const FOOTER_HEIGHT = 32;
export const OVERSCAN = 15;

// Column widths for virtualized table
export const COLUMN_WIDTHS = {
    checkbox: 40,
    index: 60,
    target: 160,
    mode: 80,
    counter: 140,
    devices: 140,
    vlans: 120,
    srcs: 200,
    dsts: 200,
    actions: 80,
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
        alignItems: 'center',
    },
    index: {
        width: COLUMN_WIDTHS.index,
        minWidth: COLUMN_WIDTHS.index,
        maxWidth: COLUMN_WIDTHS.index,
        paddingRight: 8,
        textAlign: 'right',
        color: 'var(--g-color-text-secondary)',
    },
    target: {
        width: COLUMN_WIDTHS.target,
        minWidth: COLUMN_WIDTHS.target,
        maxWidth: COLUMN_WIDTHS.target,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        userSelect: 'text',
    },
    mode: {
        width: COLUMN_WIDTHS.mode,
        minWidth: COLUMN_WIDTHS.mode,
        maxWidth: COLUMN_WIDTHS.mode,
        paddingRight: 8,
        userSelect: 'text',
    },
    counter: {
        width: COLUMN_WIDTHS.counter,
        minWidth: COLUMN_WIDTHS.counter,
        maxWidth: COLUMN_WIDTHS.counter,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        userSelect: 'text',
    },
    devices: {
        width: COLUMN_WIDTHS.devices,
        minWidth: COLUMN_WIDTHS.devices,
        maxWidth: COLUMN_WIDTHS.devices,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        userSelect: 'text',
    },
    vlans: {
        width: COLUMN_WIDTHS.vlans,
        minWidth: COLUMN_WIDTHS.vlans,
        maxWidth: COLUMN_WIDTHS.vlans,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        userSelect: 'text',
    },
    srcs: {
        width: COLUMN_WIDTHS.srcs,
        minWidth: COLUMN_WIDTHS.srcs,
        maxWidth: COLUMN_WIDTHS.srcs,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        userSelect: 'text',
    },
    dsts: {
        width: COLUMN_WIDTHS.dsts,
        minWidth: COLUMN_WIDTHS.dsts,
        maxWidth: COLUMN_WIDTHS.dsts,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        userSelect: 'text',
    },
    actions: {
        width: COLUMN_WIDTHS.actions,
        minWidth: COLUMN_WIDTHS.actions,
        maxWidth: COLUMN_WIDTHS.actions,
        paddingRight: 8,
        display: 'flex',
        justifyContent: 'center',
        alignItems: 'center',
    },
};
