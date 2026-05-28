import React from 'react';

interface IconProps {
    size?: number;
    className?: string;
}

/** X close icon. */
export const CloseIcon = ({ size = 14, className }: IconProps): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" className={className}>
        <path d="M6 6l12 12M6 18 18 6" />
    </svg>
);

/** Trash / delete icon. */
export const TrashIcon = ({ size = 14, className }: IconProps): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" className={className}>
        <path d="M5 7h14M9 7V5h6v2M7 7l1 12h8l1-12" />
    </svg>
);

/** Save / floppy disk icon. */
export const SaveIcon = ({ size = 18, className }: IconProps): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" className={className}>
        <path d="M5 5h11l3 3v11H5zM8 5v5h7V5M8 14h8v5H8z" />
    </svg>
);

/** Discard / counter-clockwise rotate arrow icon. */
export const DiscardIcon = ({ size = 18, className }: IconProps): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" className={className}>
        <path d="M3 12a9 9 0 1 0 2.636-6.364L3 8" />
        <path d="M3 3v5h5" />
    </svg>
);

/** Chevron down icon. */
export const ChevronDownIcon = ({ size = 18, className }: IconProps): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" className={className}>
        <path d="M6 9l6 6 6-6" />
    </svg>
);
