import { useEffect } from 'react';
import { usePalette } from '../components/command-palette';
import { isTypingTarget, isOverlayOpen } from '../utils/keyboard';

interface UseListNavigationOptions<T extends { id: string }> {
    /** Rows to navigate over. */
    rows: T[];
    /** The currently active row id, or null if none. */
    activeId: string | null;
    /** Called to change the active row. */
    setActiveId: (id: string | null) => void;
    /** Called when Enter is pressed on the active row. */
    onActivate?: (row: T) => void;
    /** Called when d/Backspace is pressed on the active row. Only handled when provided. */
    onDelete?: (row: T) => void;
    /** Maps a row id to a DOM element id for scrollIntoView after navigation. */
    getElementId?: (id: string) => string;
    /** When false the hook is a no-op. Defaults to true. */
    enabled?: boolean;
}

/** Adds Arrow Up/Down/Enter/Esc/d/Backspace keyboard navigation to a list of rows; arrows defer only to arrow-consuming controls (select, listbox, combobox, menu, slider, spinbutton); Enter and delete keys defer to any focused interactive control. */
export const useListNavigation = <T extends { id: string }>({
    rows,
    activeId,
    setActiveId,
    onActivate,
    onDelete,
    getElementId,
    enabled = true,
}: UseListNavigationOptions<T>): void => {
    const { open: paletteOpen, helpOpen } = usePalette();

    useEffect(() => {
        if (!enabled) return;

        const handleKeyDown = (e: KeyboardEvent): void => {
            const key = e.key;
            const isNavKey =
                key === 'ArrowDown' ||
                key === 'ArrowUp' ||
                key === 'Enter' ||
                key === 'Escape' ||
                key === 'd' ||
                key === 'Backspace';
            if (!isNavKey) return;

            if (paletteOpen || helpOpen) return;
            if (e.metaKey || e.ctrlKey || e.altKey) return;

            const target = e.target as HTMLElement;
            if (isTypingTarget(target)) {
                return;
            }

            if (isOverlayOpen()) {
                return;
            }

            if (key === 'ArrowDown' || key === 'ArrowUp') {
                // Controls that consume arrow keys themselves take precedence.
                if (target.closest('select, [role="listbox"], [role="combobox"], [role="menu"], [role="menubar"], [role="slider"], [role="spinbutton"]')) return;
                e.preventDefault();
                if (rows.length === 0) return;

                let nextId: string;
                if (activeId === null) {
                    nextId = key === 'ArrowDown' ? rows[0].id : rows[rows.length - 1].id;
                } else {
                    const idx = rows.findIndex((r) => r.id === activeId);
                    if (idx === -1) {
                        nextId = rows[0].id;
                    } else if (key === 'ArrowDown') {
                        nextId = rows[Math.min(rows.length - 1, idx + 1)].id;
                    } else {
                        nextId = rows[Math.max(0, idx - 1)].id;
                    }
                }

                setActiveId(nextId);

                if (getElementId) {
                    document.getElementById(getElementId(nextId))?.scrollIntoView({ block: 'nearest' });
                }
                return;
            }

            if (key === 'Enter') {
                // Defer activation to a focused interactive control.
                if (target.closest('button, select, a[href], [role="button"], [role="menuitem"]')) return;
                if (activeId === null) return;
                const row = rows.find((r) => r.id === activeId);
                if (row && onActivate) {
                    onActivate(row);
                }
                return;
            }

            if (key === 'Escape') {
                if (activeId === null) return;
                setActiveId(null);
                return;
            }

            if (key === 'd' || key === 'Backspace') {
                // Defer activation to a focused interactive control.
                if (target.closest('button, select, a[href], [role="button"], [role="menuitem"]')) return;
                if (!onDelete) return;
                if (activeId === null) return;
                const row = rows.find((r) => r.id === activeId);
                if (row) {
                    e.preventDefault();
                    onDelete(row);
                }
                return;
            }
        };

        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [rows, activeId, setActiveId, onActivate, onDelete, getElementId, enabled, paletteOpen, helpOpen]);
};
