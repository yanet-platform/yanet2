import React from 'react';

interface EndpointProps {
    kind: 'in' | 'out';
}

/** Arrow icon used inside endpoint circles. */
const ArrowIcon = (): React.JSX.Element => (
    <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
    >
        <path d="M5 12h14m0 0-4-4m4 4-4 4" />
    </svg>
);

/**
 * Ingress / egress endpoint circle for a lane track.
 */
export const Endpoint: React.FC<EndpointProps> = ({ kind }) => (
    <div
        className={`fn-endpoint fn-endpoint--${kind}`}
        title={kind === 'in' ? 'Ingress' : 'Egress'}
        aria-label={kind === 'in' ? 'Ingress' : 'Egress'}
    >
        <ArrowIcon />
    </div>
);
