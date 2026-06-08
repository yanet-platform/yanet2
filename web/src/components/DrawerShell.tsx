import React from 'react';

export interface DrawerShellProps {
    open: boolean;
    ariaLabel: string;
    onBackdropClick: () => void;
    title: React.ReactNode;
    headActions: React.ReactNode;
    footMeta: React.ReactNode;
    footActions: React.ReactNode;
    children: React.ReactNode;
}

/** Slide-in drawer chrome: backdrop, header with title and actions, body, and footer. */
export const DrawerShell: React.FC<DrawerShellProps> = ({
    open,
    ariaLabel,
    onBackdropClick,
    title,
    headActions,
    footMeta,
    footActions,
    children,
}) => (
    <>
        <div
            className={`yn-backdrop${open ? ' yn-backdrop--open' : ''}`}
            onClick={onBackdropClick}
            aria-hidden="true"
        />
        <aside
            className={`yn-drawer${open ? ' yn-drawer--open' : ''}`}
            role="dialog"
            aria-modal="true"
            aria-label={ariaLabel}
        >
            <header className="yn-drawer__head">
                <h2 className="yn-drawer__title">{title}</h2>
                <div className="yn-drawer__head-actions">{headActions}</div>
            </header>
            <div className="yn-drawer__body">{children}</div>
            <footer className="yn-drawer__foot">
                <span className="yn-drawer__foot-meta">{footMeta}</span>
                <div className="yn-drawer__foot-actions">{footActions}</div>
            </footer>
        </aside>
    </>
);
