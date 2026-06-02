import React, { useEffect } from 'react';

interface LaneDrawerShellProps {
    prefix: 'fn' | 'pl';
    ariaLabel: string;
    onClose: () => void;
    children: React.ReactNode;
}

/** Shared slide-in drawer chrome: backdrop, container div, and Escape-key handler. */
export const LaneDrawerShell = ({ prefix, ariaLabel, onClose, children }: LaneDrawerShellProps): React.JSX.Element => {
    useEffect(() => {
        const handleKey = (e: KeyboardEvent): void => {
            if (e.key === 'Escape') {
                onClose();
            }
        };
        document.addEventListener('keydown', handleKey);
        return () => document.removeEventListener('keydown', handleKey);
    }, [onClose]);

    return (
        <>
            <div className={`${prefix}-drawer__backdrop`} onClick={onClose} />
            <div className={`${prefix}-drawer`} role="dialog" aria-label={ariaLabel}>
                {children}
            </div>
        </>
    );
};
