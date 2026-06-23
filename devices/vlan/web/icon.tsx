import React from 'react';

/** Stacked-tag icon for VLAN (logical) devices. */
export const IconVlan = ({
    size = 16,
    color = 'currentColor',
}: {
    size?: number;
    color?: string;
}): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke={color} strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round">
        <path d="M2.5 5.5L5.5 2.5H10.5L13.5 5.5V10.5L10.5 13.5H5.5L2.5 10.5Z" opacity="0.35" />
        <path d="M4.5 7.5L7 5H11.5L13.5 7V11L11.5 13.5H7L4.5 11Z" fill={color} fillOpacity="0.12" />
        <circle cx="10.5" cy="9" r="0.9" fill={color} stroke="none" />
    </svg>
);
