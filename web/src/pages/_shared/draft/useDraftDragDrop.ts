import { useCallback, useRef, useState } from 'react';

export interface DragOverState {
    id: string | null;
    where: 'top' | 'bottom' | null;
}

export interface UseDraftDragDropResult {
    dragOverState: DragOverState;
    handleDragStart: (id: string, e: React.DragEvent) => void;
    handleDragOver: (id: string, e: React.DragEvent) => void;
    handleDragLeave: () => void;
    handleDrop: <T extends { id: string }>(
        id: string,
        e: React.DragEvent,
        rows: T[],
        onReorder: (rows: T[]) => void,
    ) => void;
}

const RESET: DragOverState = { id: null, where: null };

/**
 * Drag-and-drop state machine for reorderable virtualized draft tables.
 *
 * handleDrop computes the new row order and calls onReorder; the caller
 * passes the current rawRows and a dispatch callback each time.
 */
export function useDraftDragDrop(): UseDraftDragDropResult {
    const dragRowIdRef = useRef<string | null>(null);
    const dragOverRef = useRef<DragOverState>(RESET);
    const [dragOverState, setDragOverState] = useState<DragOverState>(RESET);

    const handleDragStart = useCallback((id: string, e: React.DragEvent): void => {
        dragRowIdRef.current = id;
        e.dataTransfer.effectAllowed = 'move';
        try { e.dataTransfer.setData('text/plain', id); } catch (_) {}
    }, []);

    const handleDragOver = useCallback((id: string, e: React.DragEvent): void => {
        e.preventDefault();
        if (!dragRowIdRef.current || dragRowIdRef.current === id) return;
        const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
        const where: 'top' | 'bottom' = (e.clientY - rect.top) < rect.height / 2 ? 'top' : 'bottom';
        dragOverRef.current = { id, where };
        setDragOverState({ id, where });
    }, []);

    const handleDragLeave = useCallback((): void => {
        dragOverRef.current = RESET;
        setDragOverState(RESET);
    }, []);

    const handleDrop = useCallback(<T extends { id: string }>(
        id: string,
        e: React.DragEvent,
        rows: T[],
        onReorder: (rows: T[]) => void,
    ): void => {
        e.preventDefault();
        const dragId = dragRowIdRef.current;
        const over = dragOverRef.current;
        dragRowIdRef.current = null;
        dragOverRef.current = RESET;
        setDragOverState(RESET);
        if (!dragId || dragId === id) return;
        const next = [...rows];
        const fromIdx = next.findIndex((r) => r.id === dragId);
        if (fromIdx < 0) return;
        const [moved] = next.splice(fromIdx, 1);
        let toIdx = next.findIndex((r) => r.id === id);
        if (toIdx < 0) return;
        if ((over.where ?? 'bottom') === 'bottom') toIdx += 1;
        next.splice(toIdx, 0, moved);
        onReorder(next);
    }, []);

    return {
        dragOverState,
        handleDragStart,
        handleDragOver,
        handleDragLeave,
        handleDrop,
    };
}
