import { useCallback, useEffect, useRef, useState } from 'react';
import { toaster } from '@gravity-ui/uikit/toaster-singleton';

export interface UseAsyncDataOptions<T> {
    /** Function that fetches the data */
    fetchFn: () => Promise<T>;
    /** Toast name for error notification */
    errorToastName: string;
    /** Error message prefix */
    errorMessage: string;
    /** Dependencies that trigger refetch when changed */
    deps?: unknown[];
    /** Whether to skip initial fetch */
    skip?: boolean;
}

export interface UseAsyncDataResult<T> {
    /** The fetched data */
    data: T | null;
    /** Loading state */
    loading: boolean;
    /** Error if fetch failed */
    error: Error | null;
    /** Manually trigger a refetch */
    refetch: () => Promise<void>;
}

/**
 * Hook for fetching async data with loading state, error handling, and cleanup
 */
export const useAsyncData = <T>({
    fetchFn,
    errorToastName,
    errorMessage,
    deps = [],
    skip = false,
}: UseAsyncDataOptions<T>): UseAsyncDataResult<T> => {
    const [data, setData] = useState<T | null>(null);
    const [loading, setLoading] = useState(!skip);
    const [error, setError] = useState<Error | null>(null);
    const isMountedRef = useRef(true);

    const fetchData = useCallback(async () => {
        if (skip) return;

        setLoading(true);
        setError(null);

        try {
            const result = await fetchFn();
            if (isMountedRef.current) {
                setData(result);
            }
        } catch (err) {
            if (isMountedRef.current) {
                const errorObj = err instanceof Error ? err : new Error('Unknown error');
                setError(errorObj);
                toaster.add({
                    name: errorToastName,
                    title: 'Error',
                    content: `${errorMessage}: ${errorObj.message}`,
                    theme: 'danger',
                    isClosable: true,
                    autoHiding: 5000,
                });
            }
        } finally {
            if (isMountedRef.current) {
                setLoading(false);
            }
        }
    }, [fetchFn, errorToastName, errorMessage, skip]);

    useEffect(() => {
        isMountedRef.current = true;
        fetchData();

        return () => {
            isMountedRef.current = false;
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [fetchData, ...deps]);

    const refetch = useCallback(async () => {
        await fetchData();
    }, [fetchData]);

    return { data, loading, error, refetch };
};

export interface UsePollingDataOptions<T> extends UseAsyncDataOptions<T> {
    /** Polling interval in milliseconds */
    interval: number;
}

/**
 * Hook for polling data at a regular interval
 */
export const usePollingData = <T>({
    fetchFn,
    errorToastName,
    errorMessage,
    deps = [],
    skip = false,
    interval,
}: UsePollingDataOptions<T>): UseAsyncDataResult<T> => {
    const [data, setData] = useState<T | null>(null);
    const [loading, setLoading] = useState(!skip);
    const [error, setError] = useState<Error | null>(null);
    const isMountedRef = useRef(true);

    const fetchData = useCallback(async (showLoader: boolean) => {
        if (skip) return;

        if (showLoader) {
            setLoading(true);
        }
        setError(null);

        try {
            const result = await fetchFn();
            if (isMountedRef.current) {
                setData(result);
            }
        } catch (err) {
            if (isMountedRef.current) {
                const errorObj = err instanceof Error ? err : new Error('Unknown error');
                setError(errorObj);
                toaster.add({
                    name: errorToastName,
                    title: 'Error',
                    content: `${errorMessage}: ${errorObj.message}`,
                    theme: 'danger',
                    isClosable: true,
                    autoHiding: 5000,
                });
            }
        } finally {
            if (isMountedRef.current && showLoader) {
                setLoading(false);
            }
        }
    }, [fetchFn, errorToastName, errorMessage, skip]);

    useEffect(() => {
        isMountedRef.current = true;
        fetchData(true);

        const intervalId = window.setInterval(() => {
            fetchData(false);
        }, interval);

        return () => {
            isMountedRef.current = false;
            window.clearInterval(intervalId);
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [fetchData, interval, ...deps]);

    const refetch = useCallback(async () => {
        await fetchData(true);
    }, [fetchData]);

    return { data, loading, error, refetch };
};

