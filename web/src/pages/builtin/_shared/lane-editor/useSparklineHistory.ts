import { useEffect, useRef, useState } from 'react';

const RING_SIZE = 60;

/**
 * Samples a value ~1/s and maintains a 60-sample ring buffer per moduleId.
 * Returns the current ring buffer as an array of numbers.
 */
const SEED_SIZE = 8;

export const useSparklineHistory = (moduleId: string, currentPps: number): number[] => {
    const bufferRef = useRef<Map<string, number[]>>(new Map());
    const [, forceRender] = useState(0);

    if (!bufferRef.current.has(moduleId)) {
        const seed: number[] = [];
        for (let i = 0; i < SEED_SIZE; i++) {
            const jitter = currentPps > 0 ? (Math.random() - 0.5) * 0.1 * currentPps : 0;
            seed.push(Math.max(0, currentPps + jitter));
        }
        bufferRef.current.set(moduleId, seed);
    }

    useEffect(() => {
        const push = (): void => {
            const map = bufferRef.current;
            const buf = map.get(moduleId) ?? [];
            const next = [...buf, currentPps];
            if (next.length > RING_SIZE) {
                next.shift();
            }
            map.set(moduleId, next);
            forceRender(n => n + 1);
        };

        const id = setInterval(push, 1000);
        return () => clearInterval(id);
    }, [moduleId, currentPps]);

    return bufferRef.current.get(moduleId) ?? [];
};
