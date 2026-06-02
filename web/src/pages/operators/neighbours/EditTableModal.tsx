import React, { useEffect, useState } from 'react';
import type { NeighbourTableInfo } from '../../../api/neighbours';

export interface EditTableModalProps {
    open: boolean;
    onClose: () => void;
    onSave: (name: string, defaultPriority: number) => Promise<void>;
    tableInfo: NeighbourTableInfo | null;
}

/** Modal for editing a neighbour table's default priority. */
const EditTableModal: React.FC<EditTableModalProps> = ({
    open,
    onClose,
    onSave,
    tableInfo,
}) => {
    const [defaultPriority, setDefaultPriority] = useState('');
    const [submitting, setSubmitting] = useState(false);

    useEffect(() => {
        if (open && tableInfo) {
            setDefaultPriority(tableInfo.default_priority?.toString() ?? '0');
            setSubmitting(false);
        }
    }, [open, tableInfo]);

    if (!open) return null;

    const priorityNum = Number(defaultPriority);
    const priorityError =
        !defaultPriority.trim() || isNaN(priorityNum) || priorityNum < 0 || !Number.isInteger(priorityNum)
            ? 'Priority must be a non-negative integer'
            : undefined;
    const canSave = !submitting && !priorityError && !!tableInfo?.name;

    const handleSave = async (): Promise<void> => {
        if (!canSave || !tableInfo?.name) return;
        setSubmitting(true);
        try {
            await onSave(tableInfo.name, priorityNum);
            onClose();
        } catch {
            setSubmitting(false);
        }
    };

    const handleClose = (): void => {
        if (submitting) return;
        onClose();
    };

    return (
        <div className="yn-modal-backdrop" onClick={handleClose}>
            <div className="yn-modal yn-modal--sm" onClick={(e) => e.stopPropagation()}>
                <header className="yn-modal__head">
                    <span className="yn-modal__title">Edit table — {tableInfo?.name}</span>
                    <button type="button" className="yn-icon-btn" onClick={handleClose} aria-label="Close">✕</button>
                </header>
                <div className="yn-modal__body yn-modal__body--confirm">
                    <div className="yn-field">
                        <label className="yn-field__label">Name</label>
                        <input
                            className="yn-input"
                            type="text"
                            value={tableInfo?.name || ''}
                            disabled
                        />
                        <span className="yn-field__hint">Name can't be changed</span>
                    </div>
                    <div className="yn-field">
                        <label className="yn-field__label" htmlFor="et-priority">
                            Default Priority <span className="yn-field__req">*</span>
                        </label>
                        <input
                            id="et-priority"
                            className={`yn-input${priorityError && defaultPriority ? ' yn-input--invalid' : ''}`}
                            type="number"
                            value={defaultPriority}
                            onChange={(e) => setDefaultPriority(e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter') handleSave();
                                if (e.key === 'Escape') handleClose();
                            }}
                            placeholder="100"
                            autoFocus
                        />
                        {priorityError && defaultPriority ? (
                            <span className="yn-field__hint yn-field__error">{priorityError}</span>
                        ) : (
                            <span className="yn-field__hint">Lower value wins on merge.</span>
                        )}
                    </div>
                </div>
                <footer className="yn-modal__foot">
                    <span />
                    <div className="yn-modal__foot-actions">
                        <button type="button" className="yn-btn yn-btn--ghost" onClick={handleClose} disabled={submitting}>
                            Cancel
                        </button>
                        <button
                            type="button"
                            className="yn-btn yn-btn--primary"
                            onClick={handleSave}
                            disabled={!canSave}
                        >
                            {submitting ? 'Saving…' : 'Save'}
                        </button>
                    </div>
                </footer>
            </div>
        </div>
    );
};

export default EditTableModal;
