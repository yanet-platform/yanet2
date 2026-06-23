import React from 'react';

/** Warning triangle icon. */
export const IconWarning = ({ size = 12 }: { size?: number }): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.2">
        <path d="M6 1.5L11 10.5H1Z" />
        <path d="M6 5v2.5M6 9v.01" strokeLinecap="round" />
    </svg>
);

/** Downward arrow, used for RX direction label. */
export const IconArrowDown = ({ size = 12 }: { size?: number }): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
        <path d="M6 2v8M3 7l3 3 3-3" />
    </svg>
);

/** Upward arrow, used for TX direction label. */
export const IconArrowUp = ({ size = 12 }: { size?: number }): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
        <path d="M6 10V2M3 5l3-3 3 3" />
    </svg>
);

/** Stacked-layers icon, used for grouping toggle. */
export const IconStack = ({ size = 12 }: { size?: number }): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.2">
        <path d="M2 4L6 2L10 4L6 6Z" />
        <path d="M2 6L6 8L10 6" />
        <path d="M2 8L6 10L10 8" />
    </svg>
);

/** Caret chevron icon. Direction prop controls rotation. */
export const IconCaret = ({ size = 12, dir = 'down' }: { size?: number; dir?: 'down' | 'up' | 'left' | 'right' }): React.JSX.Element => {
    const rotations = { down: 0, up: 180, left: 90, right: -90 };
    const rot = rotations[dir];
    return (
        <svg
            width={size}
            height={size}
            viewBox="0 0 12 12"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            style={{ transform: `rotate(${rot}deg)` }}
        >
            <path d="M3 4.5L6 7.5L9 4.5" />
        </svg>
    );
};

/** Hard-drive icon used as the empty-state illustration. */
export const IconHdd = ({ size = 16 }: { size?: number }): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3">
        <rect x="2" y="5" width="12" height="6" rx="1" />
        <circle cx="4.5" cy="8" r=".7" fill="currentColor" />
        <path d="M7 8h5" />
    </svg>
);

/** Magnifier icon used in the search box. */
export const IconSearch = ({ size = 14 }: { size?: number }): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round">
        <circle cx="7" cy="7" r="4.5" />
        <path d="M10.5 10.5L13.5 13.5" />
    </svg>
);

/** Plus / add icon. */
export const IconPlus = ({ size = 14 }: { size?: number }): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round">
        <path d="M8 3v10M3 8h10" />
    </svg>
);

/** Trash / delete icon. */
export const IconTrash = ({ size = 14 }: { size?: number }): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round">
        <path d="M3 4.5h10M6.5 4.5V3a1 1 0 011-1h1a1 1 0 011 1v1.5M4.5 4.5l.5 8a1 1 0 001 1h4a1 1 0 001-1l.5-8" />
    </svg>
);

/** Floppy disk / save icon. */
export const IconSave = ({ size = 14 }: { size?: number }): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round">
        <path d="M3.5 2.5h8L13.5 4.5v9h-11v-11z" />
        <path d="M5.5 2.5v4h5v-4M5.5 13.5V9h5v4.5" />
    </svg>
);
