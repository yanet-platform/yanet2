import React, { useCallback, useEffect, useRef, useState } from 'react';
import BpfTokens from './BpfTokens';

interface FilterRowProps {
    filter: string;
}

/**
 * Full-width card showing the active config's BPF filter with syntax highlighting,
 * overflow-fade mask, and copy-to-clipboard button.
 */
const FilterRow: React.FC<FilterRowProps> = ({ filter }) => {
    const scrollRef = useRef<HTMLDivElement>(null);
    const [overflow, setOverflow] = useState(false);
    const [copied, setCopied] = useState(false);
    const copyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    useEffect(() => {
        const el = scrollRef.current;
        if (!el) return;

        const check = () => setOverflow(el.scrollWidth > el.clientWidth);
        check();

        const ro = new ResizeObserver(check);
        ro.observe(el);
        return () => ro.disconnect();
    }, [filter]);

    const handleCopy = useCallback(() => {
        const text = filter || '';

        const doWrite = (t: string) => {
            if (navigator.clipboard && navigator.clipboard.writeText) {
                navigator.clipboard.writeText(t).catch(() => fallback(t));
            } else {
                fallback(t);
            }
        };

        const fallback = (t: string) => {
            const ta = document.createElement('textarea');
            ta.value = t;
            ta.style.position = 'fixed';
            ta.style.opacity = '0';
            document.body.appendChild(ta);
            ta.focus();
            ta.select();
            try { document.execCommand('copy'); } catch { /* best-effort */ }
            document.body.removeChild(ta);
        };

        doWrite(text);

        setCopied(true);
        if (copyTimerRef.current !== null) {
            clearTimeout(copyTimerRef.current);
        }
        copyTimerRef.current = setTimeout(() => {
            setCopied(false);
            copyTimerRef.current = null;
        }, 1300);
    }, [filter]);

    return (
        <div className="pdump-filter-card">
            <span className="pdump-filter-card__label">BPF</span>
            <div className={`pdump-filter-card__expr-wrap${overflow ? '' : ' pdump-filter-card__expr-wrap--no-overflow'}`}>
                <div className="pdump-filter-card__expr" ref={scrollRef}>
                    <BpfTokens expr={filter} />
                </div>
            </div>
            <button
                type="button"
                className={`pdump-filter-card__copy${copied ? ' pdump-filter-card__copy--copied' : ''}`}
                onClick={handleCopy}
                title="Copy filter to clipboard"
            >
                {copied ? '✓ Copied' : 'Copy'}
            </button>
        </div>
    );
};

export default React.memo(FilterRow);
