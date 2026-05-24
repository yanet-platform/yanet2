import React from 'react';

export interface IconProps {
    size?: number;
    color?: string;
}

/** Physical port icon: a small rectangle with pin lines. */
export const IconPort: React.FC<IconProps> = ({ size = 14, color = 'currentColor' }) => (
    <svg width={size} height={size} viewBox="0 0 14 14" fill="none" aria-hidden>
        <rect x="2" y="3" width="10" height="8" stroke={color} strokeWidth="1" />
        <line x1="4" y1="11" x2="4" y2="13" stroke={color} strokeWidth="1" />
        <line x1="7" y1="11" x2="7" y2="13" stroke={color} strokeWidth="1" />
        <line x1="10" y1="11" x2="10" y2="13" stroke={color} strokeWidth="1" />
        <line x1="4.5" y1="6" x2="9.5" y2="6" stroke={color} strokeWidth="0.7" />
        <line x1="4.5" y1="8" x2="9.5" y2="8" stroke={color} strokeWidth="0.7" />
    </svg>
);

/** VLAN tag icon: a dashed tag shape with a filled dot. */
export const IconTag: React.FC<IconProps> = ({ size = 14, color = 'currentColor' }) => (
    <svg width={size} height={size} viewBox="0 0 14 14" fill="none" aria-hidden>
        <path
            d="M1.5 4 L6 1 L12.5 1 L12.5 8 L6 13 L1.5 10 Z"
            stroke={color}
            strokeWidth="1"
            strokeDasharray="2 1.5"
        />
        <circle cx="10" cy="4" r="1" fill={color} />
    </svg>
);

/** Function icon: square brackets with two short horizontal lines. */
export const IconFn: React.FC<IconProps> = ({ size = 14, color = 'currentColor' }) => (
    <svg width={size} height={size} viewBox="0 0 14 14" fill="none" aria-hidden>
        <path d="M3 2 L2 2 L2 12 L3 12 M11 2 L12 2 L12 12 L11 12" stroke={color} strokeWidth="1" />
        <path d="M5 5 L9 5 M5 8 L8 8" stroke={color} strokeWidth="0.8" />
    </svg>
);
