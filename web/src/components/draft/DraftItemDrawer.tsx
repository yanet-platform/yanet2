import React from 'react';
import { useDrawerKeyboard } from '../../hooks';

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
    /** When false, Ctrl/Cmd+Enter does not apply. Defaults to true. */
    canApply?: boolean;
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
    canApply = true,
    onDelete,
    onJump,
    ariaLabel,
    children,
}) => {
    useDrawerKeyboard({ open, onClose, onApply, canApply });

    return (
    <>
        <div
            className={`yn-backdrop${open ? ' yn-backdrop--open' : ''}`}
            onClick={onClose}
            aria-hidden="true"
        />
        <aside
            className={`yn-drawer${open ? ' yn-drawer--open' : ''}`}
            role="dialog"
            aria-modal="true"
            aria-label={ariaLabel}
        >
            {open && children !== null && (
                <>
                    <header className="yn-drawer__head">
                        <h2 className="yn-drawer__title">
                            {titleVerb ?? 'Edit'} {titleSingular}
                            {!hideIndex && (
                                <>
                                    {' '}
                                    <span className="yn-drawer__rule-num">#{index + 1}</span>
                                </>
                            )}
                        </h2>
                        <div className="yn-drawer__head-actions">
                            <button
                                type="button"
                                className="yn-icon-btn"
                                onClick={() => onJump(-1)}
                                disabled={index === 0}
                                title="Previous row (↑)"
                            >
                                ↑
                            </button>
                            <button
                                type="button"
                                className="yn-icon-btn"
                                onClick={() => onJump(1)}
                                disabled={index === total - 1}
                                title="Next row (↓)"
                            >
                                ↓
                            </button>
                            {onDelete && (
                                <button
                                    type="button"
                                    className="yn-icon-btn yn-icon-btn--danger"
                                    onClick={onDelete}
                                    title="Delete row"
                                >
                                    🗑
                                </button>
                            )}
                            <button
                                type="button"
                                className="yn-icon-btn"
                                onClick={onClose}
                                aria-label="Close drawer"
                            >
                                ✕
                            </button>
                        </div>
                    </header>

                    <div className="yn-drawer__body">
                        {children}
                    </div>

                    <footer className="yn-drawer__foot">
                        <span className="yn-drawer__foot-meta">
                            Row <span className="yn-cell-mono yn-cell-strong">#{index + 1}</span> of {total}
                        </span>
                        <div className="yn-drawer__foot-actions">
                            <button type="button" className="yn-btn yn-btn--ghost" onClick={onClose}>
                                Cancel
                            </button>
                            <button
                                type="button"
                                className="yn-btn yn-btn--primary"
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
};

export default DraftItemDrawer;
