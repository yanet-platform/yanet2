import { useCallback } from 'react';
import type { SetURLSearchParams } from 'react-router-dom';

/** Batch of URL search-param mutations where null or empty string removes the key. */
export type SearchParamUpdates = Record<string, string | null>;

/** Returns stable helpers for mutating URL search params with replace navigation. */
export const useSearchParamHelpers = (
    setSearchParams: SetURLSearchParams,
    configKey = 'config',
): {
    updateParams: (updates: SearchParamUpdates) => void;
    clearConfigParamIfCurrent: (name: string) => void;
} => {
    const updateParams = useCallback((updates: SearchParamUpdates): void => {
        setSearchParams((prev) => {
            const next = new URLSearchParams(prev);
            for (const [key, value] of Object.entries(updates)) {
                if (value === null || value === '') {
                    next.delete(key);
                } else {
                    next.set(key, value);
                }
            }
            return next;
        }, { replace: true });
    }, [setSearchParams]);

    const clearConfigParamIfCurrent = useCallback((name: string): void => {
        setSearchParams((prev) => {
            if (prev.get(configKey) !== name) {
                return prev;
            }
            const next = new URLSearchParams(prev);
            next.delete(configKey);
            return next;
        }, { replace: true });
    }, [setSearchParams, configKey]);

    return { updateParams, clearConfigParamIfCurrent };
};
