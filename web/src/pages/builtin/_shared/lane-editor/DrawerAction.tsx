import React from 'react';

interface DrawerActionProps {
    prefix: 'fn' | 'pl';
    icon: React.ReactNode;
    label: string;
    danger?: boolean;
    onClick?: () => void;
}

/** Shared action button for drawer action sections. */
export const DrawerAction = ({ prefix, icon, label, danger, onClick }: DrawerActionProps): React.JSX.Element => (
    <button
        className={`${prefix}-drawer__action-btn${danger ? ` ${prefix}-drawer__action-btn--danger` : ''}`}
        type="button"
        onClick={onClick}
    >
        {icon}
        {label}
    </button>
);
