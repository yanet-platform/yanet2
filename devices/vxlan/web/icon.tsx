import React from 'react';

/** Overlay-tunnel icon for VXLAN devices: an outer capsule carrying an inner packet. */
export const IconVxlan = ({
    size = 16,
    color = 'currentColor',
}: {
    size?: number;
    color?: string;
}): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke={color} strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round">
        <rect x="1.5" y="5" width="13" height="6" rx="3" opacity="0.35" />
        <rect x="4.5" y="6.6" width="7" height="2.8" rx="1.4" fill={color} fillOpacity="0.12" />
        <circle cx="8" cy="8" r="0.9" fill={color} stroke="none" />
    </svg>
);
