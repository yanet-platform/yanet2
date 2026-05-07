import { useMemo } from 'react';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import type { PrefixItem } from './types';
import { compareNullableStrings } from '../../../utils';

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

