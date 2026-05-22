import { useCallback, useEffect, useRef, useState } from 'react';

export interface UseRowHoverOverlayResult<T> {
    /** Currently hovered row, or null when none. */
    hoveredRow: T | null;
    /** Virtualizer start offset (px) of the hovered row. */
    hoveredStart: number;
    /** Top offset of the overlay relative to .fw-tbl-wrap. */
    overlayTopOffset: number;
    /** Call from the row's onMouseEnter/onMouseLeave. Pass null to begin the hide delay. */
    handleHoverChange: (row: T | null, start: number) => void;
    /** Call from the overlay's onMouseEnter to cancel the pending hide. */
    handleOverlayMouseEnter: () => void;
    /** Call from the overlay's onMouseLeave to immediately hide. */
    handleOverlayMouseLeave: () => void;
    /** Attach to the scrollable body element to track scroll position. */
    attachScrollEl: (el: HTMLDivElement | null) => void;
}

/**
 * Manages the floating edit-button overlay state for a virtualized draft table.
 *
 * The overlay sits outside the scroll body so it does not scroll with rows.
 * Its y-position is: headerHeight + virtualizer_start - scrollTop.
 */
export function useRowHoverOverlay<T>(headerHeight: number): UseRowHoverOverlayResult<T> {
    const hideTimeoutRef = useRef<number | null>(null);
    const unlistenRef = useRef<(() => void) | null>(null);

    const [hoveredRow, setHoveredRow] = useState<T | null>(null);
    const [hoveredStart, setHoveredStart] = useState(0);
    const [bodyScrollTop, setBodyScrollTop] = useState(0);

    const attachScrollEl = useCallback((el: HTMLDivElement | null): void => {
        if (unlistenRef.current) {
            unlistenRef.current();
            unlistenRef.current = null;
        }
        if (el) {
            const onScroll = (): void => setBodyScrollTop(el.scrollTop);
            el.addEventListener('scroll', onScroll, { passive: true });
            unlistenRef.current = () => el.removeEventListener('scroll', onScroll);
        }
    }, []);

    useEffect(() => () => {
        if (hideTimeoutRef.current !== null) {
            window.clearTimeout(hideTimeoutRef.current);
        }
        if (unlistenRef.current) {
            unlistenRef.current();
        }
    }, []);

    const handleHoverChange = useCallback((row: T | null, start: number): void => {
        if (hideTimeoutRef.current !== null) {
            window.clearTimeout(hideTimeoutRef.current);
            hideTimeoutRef.current = null;
        }
        if (row === null) {
            hideTimeoutRef.current = window.setTimeout(() => {
                hideTimeoutRef.current = null;
                setHoveredRow(null);
            }, 80);
        } else {
            setHoveredRow(row);
            setHoveredStart(start);
        }
    }, []);

    const handleOverlayMouseEnter = useCallback((): void => {
        if (hideTimeoutRef.current !== null) {
            window.clearTimeout(hideTimeoutRef.current);
            hideTimeoutRef.current = null;
        }
    }, []);

    const handleOverlayMouseLeave = useCallback((): void => {
        setHoveredRow(null);
    }, []);

    const overlayTopOffset = headerHeight + hoveredStart - bodyScrollTop;

    return {
        hoveredRow,
        hoveredStart,
        overlayTopOffset,
        handleHoverChange,
        handleOverlayMouseEnter,
        handleOverlayMouseLeave,
        attachScrollEl,
    };
}
