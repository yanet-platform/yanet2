import React from 'react';

interface DraftItemDrawerProps {
    open: boolean;
    index: number;
    total: number;
    /** Singular noun for the item type, e.g. "route" or "prefix". */
    titleSingular: string;
    /** Verb shown before the noun in the drawer title. Defaults to "Edit". */
    titleVerb?: string;
    /** When true, hides the "#N" index badge in the title. Defaults to false. */
    hideIndex?: boolean;
    onClose: () => void;
    onApply: () => void;
    onDelete?: () => void;
    onJump: (delta: number) => void;
    ariaLabel: string;
    children: React.ReactNode;
}

/**
 * Shared shell for single-item draft drawers (backdrop + aside + header + footer).
 *
 * Module wrappers own local draft state, validation, and field markup.
 * They pass field sections as children and wire up onApply / onDelete.
 */
const DraftItemDrawer: React.FC<DraftItemDrawerProps> = ({
    open,
    index,
    total,
    titleSingular,
    titleVerb,
    hideIndex,
    onClose,
    onApply,
    onDelete,
    onJump,
    ariaLabel,
    children,
}) => (
    <>
        <div
            className={`fw-backdrop${open ? ' fw-backdrop--open' : ''}`}
            onClick={onClose}
            aria-hidden="true"
        />
        <aside
            className={`fw-drawer${open ? ' fw-drawer--open' : ''}`}
            role="dialog"
            aria-modal="true"
            aria-label={ariaLabel}
        >
            {open && children !== null && (
                <>
                    <header className="fw-drawer__head">
                        <h2 className="fw-drawer__title">
                            {titleVerb ?? 'Edit'} {titleSingular}
                            {!hideIndex && (
                                <>
                                    {' '}
                                    <span className="fw-drawer__rule-num">#{index + 1}</span>
                                </>
                            )}
                        </h2>
                        <div className="fw-drawer__head-actions">
                            <button
                                type="button"
                                className="fw-icon-btn"
                                onClick={() => onJump(-1)}
                                disabled={index === 0}
                                title="Previous row (↑)"
                            >
                                ↑
                            </button>
                            <button
                                type="button"
                                className="fw-icon-btn"
                                onClick={() => onJump(1)}
                                disabled={index === total - 1}
                                title="Next row (↓)"
                            >
                                ↓
                            </button>
                            {onDelete && (
                                <button
                                    type="button"
                                    className="fw-icon-btn fw-icon-btn--danger"
                                    onClick={onDelete}
                                    title="Delete row"
                                >
                                    🗑
                                </button>
                            )}
                            <button
                                type="button"
                                className="fw-icon-btn"
                                onClick={onClose}
                                aria-label="Close drawer"
                            >
                                ✕
                            </button>
                        </div>
                    </header>

                    <div className="fw-drawer__body">
                        {children}
                    </div>

                    <footer className="fw-drawer__foot">
                        <span className="fw-drawer__foot-meta">
                            Row <span className="fw-cell-mono fw-cell-strong">#{index + 1}</span> of {total}
                        </span>
                        <div className="fw-drawer__foot-actions">
                            <button type="button" className="fw-btn fw-btn--ghost" onClick={onClose}>
                                Cancel
                            </button>
                            <button
                                type="button"
                                className="fw-btn fw-btn--primary"
                                onClick={onApply}
                            >
                                Apply
                            </button>
                        </div>
                    </footer>
                </>
            )}
        </aside>
    </>
);

export default DraftItemDrawer;
