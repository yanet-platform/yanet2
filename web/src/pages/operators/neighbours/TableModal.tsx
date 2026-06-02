import React, { useEffect, useState } from 'react';
import type { NeighbourTableInfo } from '../../../api/neighbours';

interface CreateMode {
    mode: 'create';
    existingNames: string[];
    onCreate: (name: string, defaultPriority: number) => Promise<void>;
    tableInfo?: never;
    onSave?: never;
}

interface EditMode {
    mode: 'edit';
    tableInfo: NeighbourTableInfo | null;
    onSave: (name: string, defaultPriority: number) => Promise<void>;
    existingNames?: never;
    onCreate?: never;
}

export type TableModalProps = {
    open: boolean;
    onClose: () => void;
} & (CreateMode | EditMode);

/** Modal for creating or editing a neighbour table. */
const TableModal: React.FC<TableModalProps> = (props) => {
    const { open, onClose, mode } = props;

    const [name, setName] = useState('');
    const [defaultPriority, setDefaultPriority] = useState('');
    const [submitting, setSubmitting] = useState(false);

    useEffect(() => {
        if (open) {
            if (mode === 'create') {
                setName('');
                setDefaultPriority('100');
            } else if (props.tableInfo) {
                setDefaultPriority(props.tableInfo.default_priority?.toString() ?? '0');
            }
            setSubmitting(false);
        }
    }, [open, mode, mode === 'edit' ? props.tableInfo : null]);

    if (!open) return null;

    const priorityNum = Number(defaultPriority);
    const priorityError =
        !defaultPriority.trim() || isNaN(priorityNum) || priorityNum < 0 || !Number.isInteger(priorityNum)
            ? 'Priority must be a non-negative integer'
            : undefined;

    const handleClose = (): void => {
        if (submitting) return;
        onClose();
    };

    if (mode === 'create') {
        const trimmedName = name.trim();
        const nameError = submitting
            ? undefined
            : !trimmedName
                ? 'Name is required'
                : props.existingNames.includes(trimmedName)
                    ? 'A table with this name already exists'
                    : undefined;
        const canCreate = !submitting && !nameError && !priorityError;

        const handleCreate = async (): Promise<void> => {
            if (!canCreate) return;
            setSubmitting(true);
            try {
                await props.onCreate(trimmedName, priorityNum);
                onClose();
            } catch {
                setSubmitting(false);
            }
        };

        return (
            <div className="yn-modal-backdrop" onClick={handleClose}>
                <div className="yn-modal yn-modal--sm" onClick={(e) => e.stopPropagation()}>
                    <header className="yn-modal__head">
                        <span className="yn-modal__title">Create neighbour table</span>
                        <button type="button" className="yn-icon-btn" onClick={handleClose} aria-label="Close">✕</button>
                    </header>
                    <div className="yn-modal__body yn-modal__body--confirm">
                        <div className="yn-field">
                            <label className="yn-field__label" htmlFor="ct-name">
                                Name <span className="yn-field__req">*</span>
                            </label>
                            <input
                                id="ct-name"
                                className={`yn-input${nameError && trimmedName ? ' yn-input--invalid' : ''}`}
                                type="text"
                                value={name}
                                onChange={(e) => setName(e.target.value)}
                                onKeyDown={(e) => {
                                    if (e.key === 'Enter') handleCreate();
                                    if (e.key === 'Escape') handleClose();
                                }}
                                placeholder="e.g. my-table"
                                autoFocus
                            />
                            {nameError && trimmedName && (
                                <span className="yn-field__hint yn-field__error">{nameError}</span>
                            )}
                        </div>
                        <div className="yn-field">
                            <label className="yn-field__label" htmlFor="ct-priority">
                                Default Priority <span className="yn-field__req">*</span>
                            </label>
                            <input
                                id="ct-priority"
                                className={`yn-input${priorityError && defaultPriority ? ' yn-input--invalid' : ''}`}
                                type="number"
                                value={defaultPriority}
                                onChange={(e) => setDefaultPriority(e.target.value)}
                                onKeyDown={(e) => {
                                    if (e.key === 'Enter') handleCreate();
                                    if (e.key === 'Escape') handleClose();
                                }}
                                placeholder="100"
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
                                onClick={handleCreate}
                                disabled={!canCreate}
                            >
                                {submitting ? 'Creating…' : 'Create'}
                            </button>
                        </div>
                    </footer>
                </div>
            </div>
        );
    }

    const canSave = !submitting && !priorityError && !!props.tableInfo?.name;

    const handleSave = async (): Promise<void> => {
        if (!canSave || !props.tableInfo?.name) return;
        setSubmitting(true);
        try {
            await props.onSave(props.tableInfo.name, priorityNum);
            onClose();
        } catch {
            setSubmitting(false);
        }
    };

    return (
        <div className="yn-modal-backdrop" onClick={handleClose}>
            <div className="yn-modal yn-modal--sm" onClick={(e) => e.stopPropagation()}>
                <header className="yn-modal__head">
                    <span className="yn-modal__title">Edit table — {props.tableInfo?.name}</span>
                    <button type="button" className="yn-icon-btn" onClick={handleClose} aria-label="Close">✕</button>
                </header>
                <div className="yn-modal__body yn-modal__body--confirm">
                    <div className="yn-field">
                        <label className="yn-field__label">Name</label>
                        <input
                            className="yn-input"
                            type="text"
                            value={props.tableInfo?.name || ''}
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

export default TableModal;
