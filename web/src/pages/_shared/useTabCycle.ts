import { useEffect } from 'react';
import { usePalette } from '../../components/command-palette';
import { isTypingTarget, isOverlayOpen } from '../../utils/keyboard';

interface UseTabCycleOptions {
    /** Ordered list of tab identifiers. */
    tabs: string[];
    /** Currently active tab. */
    activeTab: string;
    /** Called with the new tab identifier when the user cycles. */
    onSelect: (tab: string) => void;
    /** When false, the hook does nothing. Use to gate cycling on page readiness. */
    enabled?: boolean;
}

/** Cycles config tabs with [ (prev) and ] (next) keys. No-ops when fewer than two tabs
 * are registered, when the command palette is open, when a modal or drawer overlay is
 * open (Gravity UI Dialog/Modal or the app's own yn-modal/yn-drawer variants), when a
 * modifier key (Meta, Ctrl, Alt) is held, or when the event target is an input, textarea,
 * or contentEditable element. */
export const useTabCycle = ({ tabs, activeTab, onSelect, enabled = true }: UseTabCycleOptions): void => {
    const { open: paletteOpen, helpOpen } = usePalette();

    useEffect(() => {
        if (!enabled || tabs.length < 2) return;

        const handleKeyDown = (e: KeyboardEvent): void => {
            if (e.key !== '[' && e.key !== ']') return;
            if (paletteOpen || helpOpen) return;
            if (e.metaKey || e.ctrlKey || e.altKey) return;

            const target = e.target as HTMLElement;
            if (isTypingTarget(target)) {
                return;
            }

            if (isOverlayOpen()) {
                return;
            }

            const currentIdx = tabs.indexOf(activeTab);
            if (currentIdx === -1) return;

            e.preventDefault();

            if (e.key === '[') {
                const prev = (currentIdx - 1 + tabs.length) % tabs.length;
                onSelect(tabs[prev]);
            } else {
                const next = (currentIdx + 1) % tabs.length;
                onSelect(tabs[next]);
            }
        };

        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [tabs, activeTab, onSelect, enabled, paletteOpen, helpOpen]);
};
