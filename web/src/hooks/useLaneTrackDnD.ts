import { useState, useCallback, useRef, useEffect } from 'react';

/** Options for the lane-track drag-and-drop hook. */
export interface UseLaneTrackDnDOptions<P> {
    /** True when the drag type is one this track accepts (isFunctionDrag / isPipelineDrag). */
    isItemDrag: boolean;
    /** True when a drag session is active with a non-null payload. */
    isActiveDrag: boolean;
    /** Called when a drag session should end (Escape key or successful drop). */
    onDragEnd: () => void;
    /** Returns the current drag payload from the module-level singleton. */
    getPayload: () => P | null;
    /** Returns true when this track should accept the payload; false to reject the drop. */
    acceptPayload: (payload: P) => boolean;
    /**
     * Returns the source slot index when the drag originates inside this container, or -1 otherwise.
     *
     * The hook uses this value to skip no-op drops where the item would land
     * in a slot adjacent to its current position.
     */
    sameContainerSrcIdx: (payload: P) => number;
    /** Commits the drop at the given insertion index. */
    onDropAt: (toIdx: number) => void;
}

/** Return value of useLaneTrackDnD. */
export interface UseLaneTrackDnDResult {
    activeSlotIdx: number | null;
    containerRef: React.RefObject<HTMLDivElement | null>;
    handleDragOver: (e: React.DragEvent<HTMLDivElement>) => void;
    handleDragLeave: () => void;
    handleDrop: (e: React.DragEvent<HTMLDivElement>) => void;
    handleDragEnd: () => void;
}

/**
 * Encapsulates the slot-detection drag-and-drop mechanics shared by lane-track components.
 *
 * Each caller supplies predicate callbacks that encode the divergent classification
 * logic (function drags vs pipeline drags, container identity checks) so that those
 * details remain in the component and the shared plumbing lives here.
 */
export const useLaneTrackDnD = <P,>(options: UseLaneTrackDnDOptions<P>): UseLaneTrackDnDResult => {
    const { isItemDrag, isActiveDrag, onDragEnd, getPayload, acceptPayload, sameContainerSrcIdx, onDropAt } = options;

    const [activeSlotIdx, setActiveSlotIdx] = useState<number | null>(null);
    const containerRef = useRef<HTMLDivElement>(null);
    const cancelledByEscRef = useRef(false);

    useEffect(() => {
        if (!isActiveDrag) {
            cancelledByEscRef.current = false;
            return;
        }
        const handleKeyDown = (e: KeyboardEvent): void => {
            if (e.key === 'Escape' && isActiveDrag) {
                cancelledByEscRef.current = true;
                setActiveSlotIdx(null);
                onDragEnd();
            }
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [isActiveDrag, onDragEnd]);

    const handleDragOver = useCallback((e: React.DragEvent<HTMLDivElement>): void => {
        if (!isItemDrag) {
            return;
        }
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';

        const container = containerRef.current;
        if (!container) {
            return;
        }

        const slots = container.querySelectorAll<HTMLElement>('[data-slot-idx]');
        if (slots.length === 0) {
            return;
        }

        const cx = e.clientX;
        const cy = e.clientY;
        let nearestIdx = 0;
        let nearestDist = Infinity;

        slots.forEach(slot => {
            const rect = slot.getBoundingClientRect();
            const slotCx = rect.left + rect.width / 2;
            const slotCy = rect.top + rect.height / 2;
            const dist = Math.sqrt((cx - slotCx) ** 2 + (cy - slotCy) ** 2);
            const idx = parseInt(slot.getAttribute('data-slot-idx') ?? '0', 10);
            if (dist < nearestDist) {
                nearestDist = dist;
                nearestIdx = idx;
            }
        });

        setActiveSlotIdx(nearestIdx);
    }, [isItemDrag]);

    const handleDragLeave = useCallback((): void => {
        setActiveSlotIdx(null);
    }, []);

    const handleDrop = useCallback((e: React.DragEvent<HTMLDivElement>): void => {
        e.preventDefault();
        setActiveSlotIdx(null);

        if (cancelledByEscRef.current) {
            cancelledByEscRef.current = false;
            return;
        }

        const payload = getPayload();
        if (!payload || !acceptPayload(payload)) {
            return;
        }

        if (activeSlotIdx === null) {
            return;
        }

        const toIdx = activeSlotIdx;
        const src = sameContainerSrcIdx(payload);
        if (src >= 0 && (toIdx === src || toIdx === src + 1)) {
            return;
        }

        onDropAt(toIdx);
    }, [activeSlotIdx, getPayload, acceptPayload, sameContainerSrcIdx, onDropAt]);

    const handleDragEnd = useCallback((): void => {
        setActiveSlotIdx(null);
        if (!cancelledByEscRef.current) {
            onDragEnd();
        }
    }, [onDragEnd]);

    return { activeSlotIdx, containerRef, handleDragOver, handleDragLeave, handleDrop, handleDragEnd };
};
