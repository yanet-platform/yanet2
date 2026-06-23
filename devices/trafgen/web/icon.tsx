import React from 'react';

/** Waveform icon for trafgen (traffic generator) devices. */
export const IconGenerator = ({
    size = 16,
    color = 'currentColor',
}: {
    size?: number;
    color?: string;
}): React.JSX.Element => (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke={color} strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round">
        <path d="M1.5 8h2L5 4l2 8 2-10 2 12 1.5-6h2" />
    </svg>
);
