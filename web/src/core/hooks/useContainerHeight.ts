import { useLayoutEffect, useState } from 'react';

export const useContainerHeight = (
    containerRef: React.RefObject<HTMLElement | null>,
    minHeight = 300,
    bottomOffset = 20,
): number => {
    const [height, setHeight] = useState(0);

    useLayoutEffect(() => {
        const el = containerRef.current;
        if (!el) return;

        const updateHeight = () => {
            const rect = el.getBoundingClientRect();
            const available = window.innerHeight - rect.top - bottomOffset;
            setHeight(Math.max(minHeight, available));
        };

        updateHeight();
        const resizeObserver = new ResizeObserver(updateHeight);
        resizeObserver.observe(el);
        window.addEventListener('resize', updateHeight);
        return () => {
            resizeObserver.disconnect();
            window.removeEventListener('resize', updateHeight);
        };
    }, [containerRef, minHeight, bottomOffset]);

    return height;
};
