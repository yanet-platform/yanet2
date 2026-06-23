import React from 'react';

/** NIC chip icon for physical (plain) devices. */
export const IconPlain = ({
    size = 16,
    color = 'currentColor',
}: {
    size?: number;
    color?: string;
}): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke={color} strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round">
        <rect x="3.5" y="3.5" width="9" height="9" rx="1.2" />
        <path d="M6 3.5V2M8 3.5V2M10 3.5V2M6 14v-1.5M8 14v-1.5M10 14v-1.5M3.5 6H2M3.5 8H2M3.5 10H2M14 6h-1.5M14 8h-1.5M14 10h-1.5" />
        <rect x="6" y="6" width="4" height="4" rx="0.5" fill={color} fillOpacity="0.18" stroke="none" />
    </svg>
);
