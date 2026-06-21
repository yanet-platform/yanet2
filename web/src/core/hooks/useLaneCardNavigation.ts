import { useCallback, useState } from 'react';
import { useListNavigation } from './useListNavigation';

interface UseLaneCardNavigationOptions<T extends { id: string }> {
    /** Rows to navigate over. */
    rows: T[];
    /** DOM id prefix for each card element, e.g. 'fn' yields 'fn-card-<id>'. */
    cardIdPrefix: string;
    /** Called when Enter is pressed on the active row. */
    onActivate: (row: T) => void;
}

interface LaneCardNavigation {
    /** Currently highlighted row id, or null. */
    activeId: string | null;
    /** Row id to flash after a jump, or null. */
    flashId: string | null;
    /** Flashes and scrolls the card with the given id into view. */
    jumpTo: (id: string) => void;
}

/** Keyboard navigation plus flash-and-scroll-to-card for a vertical list of lane cards. */
export const useLaneCardNavigation = <T extends { id: string }>({
    rows,
    cardIdPrefix,
    onActivate,
}: UseLaneCardNavigationOptions<T>): LaneCardNavigation => {
    const [activeId, setActiveId] = useState<string | null>(null);
    const [flashId, setFlashId] = useState<string | null>(null);

    const jumpTo = useCallback((id: string): void => {
        setFlashId(null);
        setTimeout(() => {
            setFlashId(id);
            document.getElementById(`${cardIdPrefix}-card-${id}`)?.scrollIntoView({ block: 'center', behavior: 'smooth' });
        }, 0);
    }, [cardIdPrefix]);

    useListNavigation<T>({
        rows,
        activeId,
        setActiveId,
        onActivate,
        getElementId: (id) => `${cardIdPrefix}-card-${id}`,
    });

    return { activeId, flashId, jumpTo };
};
