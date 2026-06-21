import React, { useEffect, useImperativeHandle, useRef, useState } from 'react';
import type { ChipInputHandle } from './types';

/** Core state and event handlers shared by all ChipInput variants. */
export const useChipInput = (
    value: string[],
    onChange: (values: string[]) => void,
    ref: React.Ref<ChipInputHandle>,
) => {
    const [draft, setDraft] = useState('');
    const draftRef = useRef('');
    const inputRef = useRef<HTMLInputElement>(null);

    useEffect(() => {
        draftRef.current = draft;
    }, [draft]);

    useImperativeHandle(ref, () => ({
        flush() {
            const tokens = draftRef.current
                .split(/[,\s]+/)
                .map((t) => t.trim())
                .filter(Boolean);
            if (tokens.length > 0) {
                setDraft('');
                draftRef.current = '';
            }
            return tokens;
        },
    }), []);

    const commitDraft = (raw?: string): void => {
        const source = raw ?? draft;
        const tokens = source.split(/[,\s]+/).map((t) => t.trim()).filter(Boolean);
        if (!tokens.length) return;
        onChange([...value, ...tokens]);
        setDraft('');
        draftRef.current = '';
    };

    const handleChange = (e: React.ChangeEvent<HTMLInputElement>): void => {
        const v = e.target.value;
        if (v.includes(',')) {
            commitDraft(v);
        } else {
            setDraft(v);
            draftRef.current = v;
        }
    };

    const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>): void => {
        if ((e.key === 'Enter' || e.key === 'Tab') && draft.trim()) {
            e.preventDefault();
            commitDraft();
        } else if (e.key === 'Backspace' && !draft && value.length > 0) {
            onChange(value.slice(0, -1));
        }
    };

    const handleBlur = (): void => {
        if (draft.trim()) commitDraft();
    };

    const handlePaste = (e: React.ClipboardEvent<HTMLInputElement>): void => {
        const text = e.clipboardData.getData('text');
        if (/[,\s]/.test(text)) {
            e.preventDefault();
            commitDraft(text);
        }
    };

    const startEdit = (idx: number): void => {
        const v = value[idx];
        onChange(value.filter((_, j) => j !== idx));
        setDraft(v);
        draftRef.current = v;
        setTimeout(() => inputRef.current?.focus(), 0);
    };

    const removeChip = (idx: number): void => {
        onChange(value.filter((_, j) => j !== idx));
    };

    return {
        draft,
        setDraft,
        draftRef,
        inputRef,
        commitDraft,
        handleChange,
        handleKeyDown,
        handleBlur,
        handlePaste,
        startEdit,
        removeChip,
    };
};
