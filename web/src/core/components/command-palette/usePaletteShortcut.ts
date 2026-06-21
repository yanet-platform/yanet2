import React, { useEffect } from 'react';

/** Wires the ⌘K/Ctrl+K toggle and Escape-to-close for a command palette. */
export const usePaletteShortcut = (
    open: boolean,
    setOpen: React.Dispatch<React.SetStateAction<boolean>>,
): void => {
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent): void => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
                e.preventDefault();
                setOpen((prev) => !prev);
            } else if (e.key === 'Escape' && open) {
                setOpen(false);
            }
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [open, setOpen]);
};
