import { useEffect, useImperativeHandle, useState } from 'react';
import type { Ref } from 'react';

export interface RowDraftHandle {
    flushAndApply(): boolean;
}

export interface UseRowDraftParams<Row extends { id: string }, Errors extends object> {
    open: boolean;
    row: Row | null;
    emptyErrors: Errors;
    validateRow: (row: Row) => Errors;
    onChange: (row: Row) => void;
    onClose: () => void;
    handleRef: Ref<RowDraftHandle>;
}

export interface UseRowDraftResult<Row, Errors> {
    draft: Row | null;
    errors: Errors;
    updateField: <K extends keyof Row>(key: K, val: Row[K]) => void;
    handleApply: () => void;
}

/** Generic hook that manages draft state, validation, and imperative handle for row-editing drawers. */
export const useRowDraft = <Row extends { id: string }, Errors extends object>(
    params: UseRowDraftParams<Row, Errors>,
): UseRowDraftResult<Row, Errors> => {
    const { open, row, emptyErrors, validateRow, onChange, onClose, handleRef } = params;

    const [draft, setDraft] = useState<Row | null>(null);
    const [errors, setErrors] = useState<Errors>(emptyErrors);

    useEffect(() => {
        if (open && row) {
            setDraft({ ...row });
            setErrors(emptyErrors);
        }
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open, row?.id]);

    const updateField = <K extends keyof Row>(key: K, val: Row[K]): void => {
        setDraft((prev) => {
            if (!prev) return prev;
            const next = { ...prev, [key]: val };
            setErrors(validateRow(next));
            return next;
        });
    };

    const handleApply = (): void => {
        if (!draft) return;
        onChange(draft);
        onClose();
    };

    useImperativeHandle(handleRef, () => ({
        flushAndApply() {
            if (!open || !draft) return false;
            onChange(draft);
            return true;
        },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }), [open, draft, onChange]);

    return { draft, errors, updateField, handleApply };
};
