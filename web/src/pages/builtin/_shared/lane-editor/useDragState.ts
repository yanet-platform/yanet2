import { useState } from 'react';

/** Payload describing a module being dragged between lane slots. */
export interface DragPayload {
    fromFnId: string;
    fromChainId: string;
    fromModIdx: number;
    moduleId: string;
}

/** Module-level singleton holding the active drag payload (HTML5 dataTransfer is opaque during dragover). */
let activeDragPayload: DragPayload | null = null;

export const getDragPayload = (): DragPayload | null => activeDragPayload;

export const setDragPayload = (payload: DragPayload | null): void => {
    activeDragPayload = payload;
};

export interface DragState {
    isDragging: boolean;
    dragPayload: DragPayload | null;
}

/**
 * Hook that exposes drag state as React state, synchronized with the singleton.
 * Call startDrag on dragStart and endDrag on dragEnd.
 */
export const useDragState = (): {
    dragState: DragState;
    startDrag: (payload: DragPayload) => void;
    endDrag: () => void;
} => {
    const [dragState, setDragState] = useState<DragState>({ isDragging: false, dragPayload: null });

    const startDrag = (payload: DragPayload): void => {
        setDragPayload(payload);
        setDragState({ isDragging: true, dragPayload: payload });
    };

    const endDrag = (): void => {
        setDragPayload(null);
        setDragState({ isDragging: false, dragPayload: null });
    };

    return { dragState, startDrag, endDrag };
};
