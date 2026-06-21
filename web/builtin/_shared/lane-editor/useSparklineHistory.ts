import { useEffect, useRef, useState } from 'react';

const RING_SIZE = 60;

/**
 * Samples a value ~1/s and maintains a 60-sample ring buffer per moduleId.
 * Returns the current ring buffer as an array of numbers.
 *
 * Buffer initialization is deferred until the first non-zero currentPps is
 * observed, so the consumer's Sparkline renders the dashed-baseline placeholder
 * (isFlat) instead of a flat-zero line while counters are still loading.
 *
 * The buffer entry for moduleId is deleted on unmount (or when moduleId
 * changes) to prevent unbounded memory growth.
 */
export const useSparklineHistory = (moduleId: string, currentPps: number): number[] => {
    const bufferRef = useRef<Map<string, number[]>>(new Map());
    const currentPpsRef = useRef(currentPps);
    currentPpsRef.current = currentPps;
    const [, forceRender] = useState(0);

    // Seed the buffer on the first non-zero value for this moduleId.
    // Runs inside an effect to avoid render-time side effects under react-compiler.
    useEffect(() => {
        const map = bufferRef.current;
        if (!map.has(moduleId) && currentPpsRef.current > 0) {
            map.set(moduleId, [currentPpsRef.current]);
            forceRender(n => n + 1);
        }
        return () => {
            bufferRef.current.delete(moduleId);
        };
    }, [moduleId]);

    useEffect(() => {
        const push = (): void => {
            const map = bufferRef.current;
            const pps = currentPpsRef.current;

            // Skip recording while no data has arrived yet.
            if (!map.has(moduleId) && pps === 0) {
                return;
            }

            const buf = map.get(moduleId) ?? [];
            const next = [...buf, pps];
            if (next.length > RING_SIZE) {
                next.shift();
            }
            map.set(moduleId, next);
            forceRender(n => n + 1);
        };

        const id = setInterval(push, 1000);
        return () => clearInterval(id);
    }, [moduleId]);

    return bufferRef.current.get(moduleId) ?? [];
};
