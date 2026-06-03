import { useEffect, useState } from 'react';

/** Command-palette open state plus keyboard wiring: Cmd/Ctrl+K toggles, Escape closes while open. */
export const useCommandPalette = () => {
    const [paletteOpen, setPaletteOpen] = useState(false);

    useEffect(() => {
        if (!paletteOpen) return;
        const handleKeyDown = (e: KeyboardEvent): void => {
            if (e.key === 'Escape') setPaletteOpen(false);
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [paletteOpen]);

    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent): void => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
                e.preventDefault();
                setPaletteOpen((prev) => !prev);
            }
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, []);

    return { paletteOpen, setPaletteOpen };
};
