import type React from 'react';

// Table dimensions
export const ROW_HEIGHT = 36;
export const HEADER_HEIGHT = 44;
export const SEARCH_BAR_HEIGHT = 52;
export const FOOTER_HEIGHT = 32;
export const OVERSCAN = 15;

// Column widths for virtualized neighbour table
export const COLUMN_WIDTHS = {
    checkbox: 40,
    index: 70,
    next_hop: 280,
    link_addr: 170,
    hardware_addr: 170,
    device: 100,
    state: 100,
    source: 100,
    priority: 80,
    updated_at: 200,
    actions: 60,
} as const;

export const TOTAL_WIDTH = Object.values(COLUMN_WIDTHS).reduce((a, b) => a + b, 0);

const makeCellStyle = (width: number, extra?: React.CSSProperties): React.CSSProperties => ({
    width,
    minWidth: width,
    maxWidth: width,
    paddingRight: 8,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    userSelect: 'text',
    ...extra,
});

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
    next_hop: makeCellStyle(COLUMN_WIDTHS.next_hop),
    link_addr: makeCellStyle(COLUMN_WIDTHS.link_addr),
    hardware_addr: makeCellStyle(COLUMN_WIDTHS.hardware_addr),
    device: makeCellStyle(COLUMN_WIDTHS.device),
    state: makeCellStyle(COLUMN_WIDTHS.state),
    source: makeCellStyle(COLUMN_WIDTHS.source),
    priority: makeCellStyle(COLUMN_WIDTHS.priority),
    updated_at: makeCellStyle(COLUMN_WIDTHS.updated_at),
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
