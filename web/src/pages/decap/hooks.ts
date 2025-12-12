import { useMemo, useState, useLayoutEffect } from 'react';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import type { PrefixItem } from './types';
import { compareNullableStrings } from '../../utils/sorting';

/**
 * Hook that returns table column configuration for prefixes
 */
export const usePrefixColumns = (): TableColumnConfig<PrefixItem>[] => {
    return useMemo(() => [
        {
            id: 'prefix',
            name: 'Prefix',
            meta: {
                sort: (a: PrefixItem, b: PrefixItem) => compareNullableStrings(a.prefix, b.prefix),
            },
            template: (item: PrefixItem) => item.prefix,
        },
    ], []);
};

/**
 * Convert string prefixes to PrefixItem array
 */
export const prefixesToItems = (prefixes: string[]): PrefixItem[] => {
    return prefixes.map((prefix) => ({
        id: prefix,
        prefix,
    }));
};

/**
 * Hook for measuring container height
 */
export const useContainerHeight = (containerRef: React.RefObject<HTMLDivElement | null>) => {
    const [containerHeight, setContainerHeight] = useState(0);

    useLayoutEffect(() => {
        const updateHeight = () => {
            if (containerRef.current) {
                const rect = containerRef.current.getBoundingClientRect();
                const availableHeight = window.innerHeight - rect.top - 20;
                setContainerHeight(Math.max(300, availableHeight));
            }
        };

        updateHeight();
        window.addEventListener('resize', updateHeight);

        return () => {
            window.removeEventListener('resize', updateHeight);
        };
    }, [containerRef]);

    return containerHeight;
};
