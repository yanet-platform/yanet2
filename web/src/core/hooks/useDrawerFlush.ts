import { useCallback, useImperativeHandle } from 'react';
import type { Ref, RefObject } from 'react';

/** Minimal structural type for a chip input's imperative flush handle. */
interface FlushHandle {
    flush: () => string[];
}

interface DrawerFlushRefs {
    deviceNames: RefObject<FlushHandle | null>;
    sourceCidrs: RefObject<FlushHandle | null>;
    dstCidrs: RefObject<FlushHandle | null>;
}

interface UseDrawerFlushParams<T extends { deviceNames: string[]; sourceCidrs: string[]; dstCidrs: string[] }> {
    draft: T;
    setDraft: (next: T) => void;
    onSave: (next: T) => void;
    refs: DrawerFlushRefs;
    handleRef: Ref<{ flushAndApply: () => boolean }>;
    open: boolean;
    /** When false the imperative flushAndApply returns false without applying. Defaults to true. */
    canApply?: boolean;
}

interface UseDrawerFlushResult<T> {
    buildFlushedDraft: (base: T) => T;
    handleApply: () => void;
}

/**
 * Extracts the shared flush-pending-chip-text-then-apply logic used by rule drawers.
 */
export const useDrawerFlush = <T extends { deviceNames: string[]; sourceCidrs: string[]; dstCidrs: string[] }>({
    draft,
    setDraft,
    onSave,
    refs,
    handleRef,
    open,
    canApply,
}: UseDrawerFlushParams<T>): UseDrawerFlushResult<T> => {
    const buildFlushedDraft = (base: T): T => ({
        ...base,
        deviceNames: [...base.deviceNames, ...(refs.deviceNames.current?.flush() ?? [])],
        sourceCidrs: [...base.sourceCidrs, ...(refs.sourceCidrs.current?.flush() ?? [])],
        dstCidrs:    [...base.dstCidrs,    ...(refs.dstCidrs.current?.flush()    ?? [])],
    });

    const handleApply = useCallback((): void => {
        const finalDraft = buildFlushedDraft(draft);
        setDraft(finalDraft);
        onSave(finalDraft);
    }, [draft, onSave]);

    useImperativeHandle(handleRef, () => ({
        flushAndApply() {
            if (!open || canApply === false) return false;
            handleApply();
            return true;
        },
    }), [open, canApply, handleApply]);

    return { buildFlushedDraft, handleApply };
};
