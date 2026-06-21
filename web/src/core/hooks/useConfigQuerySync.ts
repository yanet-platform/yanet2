import { useEffect } from 'react';

/** Options for the useConfigQuerySync hook. */
export interface UseConfigQuerySyncOptions {
    currentConfig: string;
    loading: boolean;
    queryConfig: string | null;
    paramKey: string;
    searchParams: URLSearchParams;
    updateParams: (updates: Record<string, string | null>) => void;
}

/** Keeps the given URL query param in sync with the currently selected config. */
export const useConfigQuerySync = ({
    currentConfig, loading, queryConfig, paramKey, searchParams, updateParams,
}: UseConfigQuerySyncOptions): void => {
    useEffect(() => {
        const updates: Record<string, string | null> = {};
        if (!loading) {
            if (!currentConfig) {
                if (searchParams.get(paramKey) !== null) {
                    updates[paramKey] = null;
                }
            } else if (queryConfig !== currentConfig) {
                updates[paramKey] = currentConfig;
            }
        }
        if (Object.keys(updates).length > 0) {
            updateParams(updates);
        }
    }, [currentConfig, loading, queryConfig, paramKey, searchParams, updateParams]);
};
