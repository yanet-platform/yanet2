import React from 'react';

interface AnyBadgeProps {
    label: string;
}

/** Muted pill badge used for wildcard values such as "any". */
const AnyBadge: React.FC<AnyBadgeProps> = ({ label }) => (
    <span className="fw-badge-any">{label}</span>
);

export default AnyBadge;
