import { useEffect } from 'react';

/** Register page-level keyboard shortcuts: Escape closes the drawer, n opens the new-rule form. */
export const usePageKeyboardShortcuts = (opts: {
    onNewRule: () => void;
    onEscape: () => void;
    drawerOpen: boolean;
}): void => {
    const { onNewRule, onEscape, drawerOpen } = opts;

    useEffect(() => {
        const onKey = (e: KeyboardEvent): void => {
            // Escape always closes the drawer regardless of focus position.
            if (e.key === 'Escape' && drawerOpen) {
                onEscape();
                return;
            }
            // n is gated: do not fire when focus is inside a text field.
            const tag = (e.target as HTMLElement).tagName;
            if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
            if (e.key === 'n' && !e.metaKey && !e.ctrlKey && !e.altKey) {
                onNewRule();
            }
        };
        window.addEventListener('keydown', onKey);
        return () => window.removeEventListener('keydown', onKey);
    }, [onNewRule, onEscape, drawerOpen]);
};
