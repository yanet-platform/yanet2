import React from 'react';

/** Small v4/v6 family badge derived from an IP string. Detects ':' for IPv6, else IPv4. */
export const FamilyBadge: React.FC<{ address: string }> = ({ address }) => {
    const isV6 = address.includes(':');
    return (
        <span
            style={{
                display: 'inline-block',
                fontSize: 9.5,
                fontWeight: 700,
                fontFamily: 'var(--yn-font-mono)',
                color: isV6 ? 'var(--g-color-text-info)' : 'var(--g-color-text-warning)',
                opacity: 0.8,
                width: 16,
                flexShrink: 0,
                whiteSpace: 'nowrap',
            }}
        >
            {isV6 ? 'v6' : 'v4'}
        </span>
    );
};
