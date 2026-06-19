import { useEffect, useRef } from 'react';

/** Options for the shared drawer keyboard hook. */
export interface UseDrawerKeyboardOptions {
    /** Whether the drawer is currently open. */
    open: boolean;
    /** Closes the drawer (bound to Escape). */
    onClose: () => void;
    /**
     * Applies / saves the drawer (bound to Ctrl/Cmd+Enter).
     *
     * Omit for read-only or view-only drawers — Ctrl/Cmd+Enter then does
     * nothing. The callback is expected to close the drawer on success so
     * focus returns to the table.
     */
    onApply?: () => void;
    /** When false, Ctrl/Cmd+Enter is ignored. Defaults to true. */
    canApply?: boolean;
}

/** Matches the open drawer chrome shared by every drawer variant. */
const OPEN_DRAWER_SELECTOR = '.yn-drawer--open';

/** First form field, then any focusable, inside the open drawer. */
const focusFirstInDrawer = (drawer: HTMLElement): boolean => {
    const field = drawer.querySelector<HTMLElement>(
        'input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [contenteditable="true"]',
    );
    const target = field ?? drawer.querySelector<HTMLElement>(
        'button:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])',
    );
    if (!target) {
        return false;
    }
    target.focus();
    return true;
};

/**
 * Wires the uniform keyboard contract shared by every table edit drawer.
 *
 * While open: Escape closes the drawer, Ctrl/Cmd+Enter applies it, and Tab
 * from outside the drawer moves focus into its first field (so the user can
 * navigate the table, then Tab into the editor). On close, focus is moved off
 * any field still inside the drawer so the table keyboard handlers resume.
 */
export const useDrawerKeyboard = ({
    open,
    onClose,
    onApply,
    canApply = true,
}: UseDrawerKeyboardOptions): void => {
    // Keep callbacks in a ref so the listener sees the latest values without
    // re-binding on every render (react-compiler safe).
    const latest = useRef({ onClose, onApply, canApply });
    latest.current = { onClose, onApply, canApply };

    useEffect(() => {
        if (!open) {
            return;
        }

        const handler = (e: KeyboardEvent): void => {
            if (e.key === 'Escape') {
                e.preventDefault();
                latest.current.onClose();
                return;
            }

            if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
                const { onApply: apply, canApply: can } = latest.current;
                if (apply && can) {
                    e.preventDefault();
                    apply();
                }
                return;
            }

            if (e.key === 'Tab') {
                const drawer = document.querySelector<HTMLElement>(OPEN_DRAWER_SELECTOR);
                if (!drawer) {
                    return;
                }
                const active = document.activeElement as HTMLElement | null;
                if (active && drawer.contains(active)) {
                    return;
                }
                if (focusFirstInDrawer(drawer)) {
                    e.preventDefault();
                }
            }
        };

        window.addEventListener('keydown', handler);
        return () => window.removeEventListener('keydown', handler);
    }, [open]);

    // When the drawer closes, release focus from any field still inside it so
    // the table's window-level keyboard handlers (arrows, Enter) resume.
    useEffect(() => {
        if (open) {
            return;
        }
        // The drawer has just lost its --open modifier, so match the base class.
        const active = document.activeElement as HTMLElement | null;
        if (active && active.closest('.yn-drawer')) {
            active.blur();
        }
    }, [open]);
};
