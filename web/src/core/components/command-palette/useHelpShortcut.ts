import React, { useEffect } from 'react';
import { isTypingTarget, isOverlayOpen } from '../../utils/keyboard';

/** Wires the ? key to toggle the keyboard-shortcuts help overlay, and Escape to close it. */
export const useHelpShortcut = (
    paletteOpen: boolean,
    helpOpen: boolean,
    setHelpOpen: React.Dispatch<React.SetStateAction<boolean>>,
): void => {
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent): void => {
            if (e.key === 'Escape' && helpOpen) {
                setHelpOpen(false);
                return;
            }

            if (e.key === '?') {
                if (paletteOpen) return;
                if (e.metaKey || e.ctrlKey || e.altKey) return;

                const target = e.target as HTMLElement;
                if (isTypingTarget(target)) {
                    return;
                }

                if (isOverlayOpen()) return;
                e.preventDefault();
                setHelpOpen((prev) => !prev);
            }
        };

        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [paletteOpen, helpOpen, setHelpOpen]);
};
