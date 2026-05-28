import { useEffect } from 'react';

interface UseDraftShortcutsOpts<T extends { id: string }> {
    rows: T[];
    activeRowId: string | null;
    setActiveRowId: (id: string | null) => void;
    editingRowId: string | null;
    setEditingRowId: (id: string | null) => void;
    onDeleteRow: (row: T) => void;
}

/**
 * Registers keyboard shortcuts shared across draft-table pages.
 *
 * Escape closes the drawer; Arrow keys navigate rows; Enter opens the drawer;
 * and d / Backspace deletes the active row.
 */
export function useDraftShortcuts<T extends { id: string }>({
    rows,
    activeRowId,
    setActiveRowId,
    editingRowId,
    setEditingRowId,
    onDeleteRow,
}: UseDraftShortcutsOpts<T>): void {
    useEffect(() => {
        const onKey = (e: KeyboardEvent): void => {
            if (e.key === 'Escape') {
                if (editingRowId) { setEditingRowId(null); return; }
            }
            const tag = (e.target as HTMLElement).tagName;
            if (tag === 'INPUT' || tag === 'TEXTAREA') return;
            if (!rows.length) return;
            if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
                e.preventDefault();
                const idx = rows.findIndex((r) => r.id === activeRowId);
                const next = e.key === 'ArrowDown'
                    ? Math.min(rows.length - 1, idx + 1)
                    : Math.max(0, idx - 1);
                if (rows[next]) setActiveRowId(rows[next].id);
                return;
            }
            if (e.key === 'Enter' && activeRowId) {
                setEditingRowId(activeRowId);
                return;
            }
            if ((e.key === 'd' || e.key === 'Backspace') && activeRowId) {
                e.preventDefault();
                const row = rows.find((r) => r.id === activeRowId);
                if (row) onDeleteRow(row);
            }
        };
        window.addEventListener('keydown', onKey);
        return () => window.removeEventListener('keydown', onKey);
    }, [editingRowId, activeRowId, rows, onDeleteRow, setActiveRowId, setEditingRowId]);
}
