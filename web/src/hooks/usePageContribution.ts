import { useEffect } from 'react';
import { usePalette } from '../components/command-palette';
import type { PagePaletteContribution } from '../components/command-palette';

/** Registers the active page's command-palette contribution and clears it on unmount. */
export const usePageContribution = (contribution: PagePaletteContribution): void => {
    const { setPageContribution } = usePalette();
    useEffect(() => {
        setPageContribution(contribution);
        return () => setPageContribution(null);
    }, [contribution, setPageContribution]);
};
