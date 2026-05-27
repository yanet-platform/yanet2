import { useEffect, useState } from 'react';
import { API } from '../../../api';

/**
 * Infer total worker count from a device counter response. Each counter
 * instance corresponds to one dataplane (NUMA) instance, and each value
 * slot in `values[]` corresponds to one worker on that instance. Polling
 * a single device is enough — every counter is sized the same way.
 *
 * Returns null while the first fetch is in flight, or if no device names
 * are available, or on error.
 */
export const useWorkerCount = (deviceNames: string[]): number | null => {
    const [count, setCount] = useState<number | null>(null);
    const firstDevice = deviceNames[0] ?? '';

    useEffect(() => {
        if (!firstDevice) {
            setCount(null);
            return;
        }
        let cancelled = false;
        (async () => {
            try {
                const resp = await API.counters.byTags({
                    tags: [
                        { key: 'device', value: firstDevice },
                        { key: 'pipeline', value: '' },
                    ],
                    query: ['rx'],
                });
                const counter = resp.groups?.[0]?.counters?.[0];
                if (!counter?.instances) {
                    if (!cancelled) setCount(null);
                    return;
                }
                let total = 0;
                for (const inst of counter.instances) {
                    total += inst.values?.length ?? 0;
                }
                if (!cancelled) setCount(total > 0 ? total : null);
            } catch {
                if (!cancelled) setCount(null);
            }
        })();
        return () => { cancelled = true; };
    }, [firstDevice]);

    return count;
};
