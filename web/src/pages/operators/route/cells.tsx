import React from 'react';
import { ROUTE_SOURCES } from './utils';

/** Inline checkmark SVG for the Best column. */
const CheckIcon: React.FC = () => (
    <svg width={13} height={13} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.4} strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block', flexShrink: 0 }}>
        <path d="M20 6 9 17l-5-5" />
    </svg>
);

/** Green "Best" indicator shown in the Best column for the best route. */
export const BestPill: React.FC<{ isBest: boolean }> = ({ isBest }) => {
    if (!isBest) {
        return <span style={{ color: 'var(--fw-text-3)', fontSize: 12.5 }}>—</span>;
    }
    return (
        <span
            style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 5,
                fontSize: 12.5,
                fontWeight: 600,
                color: 'var(--g-color-text-positive)',
                whiteSpace: 'nowrap',
            }}
        >
            <CheckIcon /> Best
        </span>
    );
};

/** Source chip: BIRD in blue, Static in amber, Unknown muted — with leading colored dot. */
export const SourceChip: React.FC<{ source: number | undefined }> = ({ source }) => {
    const label = source !== undefined ? (ROUTE_SOURCES[source] ?? '::') : '—';
    if (label === 'BIRD') {
        return (
            <span
                style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: 6,
                    padding: '2px 9px 2px 7px',
                    borderRadius: 999,
                    fontSize: 11.5,
                    fontWeight: 600,
                    letterSpacing: '0.02em',
                    color: 'var(--g-color-text-info)',
                    background: 'color-mix(in srgb, var(--g-color-text-info) 12%, transparent)',
                    whiteSpace: 'nowrap',
                    fontFamily: 'var(--fw-font-mono)',
                }}
            >
                <span style={{ width: 5, height: 5, borderRadius: 999, background: 'var(--g-color-text-info)', flexShrink: 0 }} />
                {label}
            </span>
        );
    }
    if (label === 'Static') {
        return (
            <span
                style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: 6,
                    padding: '2px 9px 2px 7px',
                    borderRadius: 999,
                    fontSize: 11.5,
                    fontWeight: 600,
                    letterSpacing: '0.02em',
                    color: 'var(--fw-accent)',
                    background: 'var(--fw-accent-soft)',
                    whiteSpace: 'nowrap',
                    fontFamily: 'var(--fw-font-mono)',
                }}
            >
                <span style={{ width: 5, height: 5, borderRadius: 999, background: 'var(--fw-accent)', flexShrink: 0 }} />
                {label}
            </span>
        );
    }
    return <span style={{ color: 'var(--fw-text-3)', fontFamily: 'var(--fw-font-mono)' }}>—</span>;
};

/** Small v4/v6 family badge derived from the prefix string. */
export const FamilyBadge: React.FC<{ prefix: string }> = ({ prefix }) => {
    const isV6 = prefix.includes(':');
    return (
        <span
            style={{
                display: 'inline-block',
                fontSize: 9.5,
                fontWeight: 700,
                fontFamily: 'var(--fw-font-mono)',
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

/** Badge shown when a prefix has multiple candidate routes. */
export const ConflictBadge: React.FC<{ count: number }> = ({ count }) => {
    if (count <= 1) return null;
    return (
        <span
            style={{
                display: 'inline-block',
                padding: '1px 6px',
                borderRadius: 999,
                fontSize: 10.5,
                fontFamily: 'var(--fw-font-mono)',
                fontWeight: 700,
                color: 'var(--fw-accent)',
                background: 'var(--fw-accent-soft)',
                whiteSpace: 'nowrap',
                flexShrink: 0,
            }}
            title={`${count} candidate routes for this prefix`}
        >
            ×{count}
        </span>
    );
};
