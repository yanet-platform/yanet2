import React, { createContext, useCallback, useContext, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { Command, RowAdapter } from './types';
import { usePaletteShortcut } from './usePaletteShortcut';

/** The contribution that the active page registers with the global palette. */
export interface PagePaletteContribution {
    commands?: Command[];
    dynamicCommands?: (query: string) => Command[];
    rowAdapter?: RowAdapter<unknown>;
    placeholder?: string;
}

interface PaletteContextValue {
    open: boolean;
    openPalette: () => void;
    closePalette: () => void;
    /** Register (or clear) the active page's palette contribution. */
    setPageContribution: (contribution: PagePaletteContribution | null) => void;
    /** The snapshotted page contribution at the time the palette was opened, or null. */
    contribution: PagePaletteContribution | null;
}

const PaletteContext = createContext<PaletteContextValue>({
    open: false,
    openPalette: () => {},
    closePalette: () => {},
    setPageContribution: () => {},
    contribution: null,
});

interface PaletteProviderProps {
    children: React.ReactNode;
}

/** Provides the global palette open state and page-contribution API. */
export const PaletteProvider: React.FC<PaletteProviderProps> = ({ children }) => {
    const [open, setOpen] = useState(false);
    const contributionRef = useRef<PagePaletteContribution | null>(null);
    const [activeContribution, setActiveContribution] = useState<PagePaletteContribution | null>(null);

    usePaletteShortcut(open, setOpen);

    const openPalette = useCallback(() => setOpen(true), []);
    const closePalette = useCallback(() => setOpen(false), []);

    // Writing the ref is always stable and never triggers a re-render, so pages
    // whose memos produce a new identity on every render cannot cause a loop.
    const setPageContribution = useCallback((c: PagePaletteContribution | null) => {
        contributionRef.current = c;
    }, []);

    // Snapshot the live ref into state when the palette opens or closes so the
    // rendered palette always sees a consistent contribution without any flash.
    useLayoutEffect(() => {
        setActiveContribution(open ? contributionRef.current : null);
    }, [open]);

    const value = useMemo((): PaletteContextValue => ({
        open,
        openPalette,
        closePalette,
        setPageContribution,
        contribution: activeContribution,
    }), [open, openPalette, closePalette, setPageContribution, activeContribution]);

    return (
        <PaletteContext.Provider value={value}>
            {children}
        </PaletteContext.Provider>
    );
};

/** Access the global palette open state and page-contribution API. */
export const usePalette = (): PaletteContextValue => useContext(PaletteContext);
