import { useEffect } from 'react';

/** Registers the page-level "n opens the new-rule form" shortcut. */
export const usePageKeyboardShortcuts = (opts: {
    onNewRule: () => void;
}): void => {
    const { onNewRule } = opts;

    useEffect(() => {
        const onKey = (e: KeyboardEvent): void => {
            // n is gated: do not fire when focus is inside a text field.
            const tag = (e.target as HTMLElement).tagName;
            if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
            if (e.key === 'n' && !e.metaKey && !e.ctrlKey && !e.altKey) {
                onNewRule();
            }
        };
        window.addEventListener('keydown', onKey);
        return () => window.removeEventListener('keydown', onKey);
    }, [onNewRule]);
};
