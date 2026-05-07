import type React from 'react';

// Table dimensions
export const ROW_HEIGHT = 40;
export const HEADER_HEIGHT = 44;
export const SEARCH_BAR_HEIGHT = 52;
export const FOOTER_HEIGHT = 32;
export const OVERSCAN = 15;

// Column widths for virtualized table
export const COLUMN_WIDTHS = {
    index: 60,
    srcs: 200,
    dsts: 200,
    srcPorts: 140,
    dstPorts: 140,
    protocols: 100,
    vlans: 100,
    devices: 140,
    counter: 120,
    action: 100,
} as const;

export const TOTAL_WIDTH = Object.values(COLUMN_WIDTHS).reduce((a, b) => a + b, 0);

// Action labels
export const ACTION_LABELS: Record<number, string> = {
    0: 'PASS',
    1: 'DENY',
    2: 'COUNT',
    3: 'CHECK_STATE',
    4: 'CREATE_STATE',
};

// Pre-computed cell styles for virtualized table
export const cellStyles: Record<keyof typeof COLUMN_WIDTHS, React.CSSProperties> = {
    index: {
        width: COLUMN_WIDTHS.index,
        minWidth: COLUMN_WIDTHS.index,
        maxWidth: COLUMN_WIDTHS.index,
        paddingRight: 8,
        textAlign: 'right',
        color: 'var(--g-color-text-secondary)',
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
    srcPorts: {
        width: COLUMN_WIDTHS.srcPorts,
        minWidth: COLUMN_WIDTHS.srcPorts,
        maxWidth: COLUMN_WIDTHS.srcPorts,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        userSelect: 'text',
    },
    dstPorts: {
        width: COLUMN_WIDTHS.dstPorts,
        minWidth: COLUMN_WIDTHS.dstPorts,
        maxWidth: COLUMN_WIDTHS.dstPorts,
        paddingRight: 8,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        userSelect: 'text',
    },
    protocols: {
        width: COLUMN_WIDTHS.protocols,
        minWidth: COLUMN_WIDTHS.protocols,
        maxWidth: COLUMN_WIDTHS.protocols,
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
    action: {
        width: COLUMN_WIDTHS.action,
        minWidth: COLUMN_WIDTHS.action,
        maxWidth: COLUMN_WIDTHS.action,
        paddingRight: 8,
        userSelect: 'text',
    },
};
