import React, { useCallback, useEffect, useRef, useState } from 'react';
import { fuzzyMatch } from './fuzzy';
import type { Command, RowAdapter } from './types';
import './command-palette.scss';

interface PaletteItem {
    id: string;
    icon: string;
    label: string;
    labelRanges: Array<[number, number]>;
    sub?: string;
    group?: string;
    onSelect: () => void;
}

export interface CommandPaletteProps<T> {
    open: boolean;
    onClose: () => void;
    placeholder?: string;
    commands: Command[];
    dynamicCommands?: (query: string) => Command[];
    rowAdapter?: RowAdapter<T>;
}

/** Renders highlighted label text with matched ranges wrapped in mark elements. */
const HighlightedLabel: React.FC<{ label: string; ranges: Array<[number, number]> }> = ({ label, ranges }) => {
    if (ranges.length === 0) {
        return <>{label}</>;
    }

    const parts: React.ReactNode[] = [];
    let cursor = 0;

    for (let k = 0; k < ranges.length; k++) {
        const [start, end] = ranges[k];
        if (cursor < start) {
            parts.push(<span key={`pre-${k}`}>{label.slice(cursor, start)}</span>);
        }
        parts.push(<mark key={`hl-${k}`} className="cp-hl">{label.slice(start, end)}</mark>);
        cursor = end;
    }

    if (cursor < label.length) {
        parts.push(<span key="tail">{label.slice(cursor)}</span>);
    }

    return <>{parts}</>;
};

/** Generic ⌘K command palette with fuzzy search over commands and row data. */
const CommandPalette = <T,>({
    open,
    onClose,
    placeholder = 'Search…',
    commands,
    dynamicCommands,
    rowAdapter,
}: CommandPaletteProps<T>): React.JSX.Element | null => {
    const [query, setQuery] = useState('');
    const [activeIdx, setActiveIdx] = useState(0);
    const inputRef = useRef<HTMLInputElement>(null);
    const listRef = useRef<HTMLDivElement>(null);

    const items: PaletteItem[] = [];

    const q = query.trim();

    if (q && dynamicCommands) {
        const dynCmds = dynamicCommands(q);
        for (const cmd of dynCmds) {
            items.push({
                id: cmd.id,
                icon: cmd.icon,
                label: cmd.label,
                labelRanges: [],
                sub: cmd.sub,
                onSelect: cmd.onSelect,
            });
        }
    }

    if (!q) {
        for (const cmd of commands) {
            items.push({
                id: cmd.id,
                icon: cmd.icon,
                label: cmd.label,
                labelRanges: [],
                sub: cmd.sub,
                group: cmd.group,
                onSelect: cmd.onSelect,
            });
        }
    } else {
        const scoredPage: Array<{ item: PaletteItem; score: number }> = [];
        const scoredNav: Array<{ item: PaletteItem; score: number }> = [];
        for (const cmd of commands) {
            const searchTarget = cmd.label + (cmd.keywords ? ' ' + cmd.keywords : '');
            const match = fuzzyMatch(q, searchTarget);
            if (match !== null) {
                const labelMatch = fuzzyMatch(q, cmd.label);
                const entry = {
                    item: {
                        id: cmd.id,
                        icon: cmd.icon,
                        label: cmd.label,
                        labelRanges: labelMatch ? labelMatch.ranges : [],
                        sub: cmd.sub,
                        group: cmd.group,
                        onSelect: cmd.onSelect,
                    },
                    score: match.score,
                };
                if (cmd.group === 'Go to') {
                    scoredNav.push(entry);
                } else {
                    scoredPage.push(entry);
                }
            }
        }
        scoredPage.sort((a, b) => b.score - a.score);
        scoredNav.sort((a, b) => b.score - a.score);
        for (const entry of scoredPage) {
            items.push(entry.item);
        }
        for (const entry of scoredNav) {
            items.push(entry.item);
        }
    }

    if (q && rowAdapter) {
        const icon = rowAdapter.icon ?? '→';
        const max = rowAdapter.max ?? 7;
        const rowScored: Array<{ item: PaletteItem; score: number }> = [];

        for (const row of rowAdapter.rows) {
            const match = fuzzyMatch(q, rowAdapter.searchText(row));
            if (match !== null) {
                const id = rowAdapter.getId(row);
                const label = rowAdapter.getLabel(row);
                const labelMatch = fuzzyMatch(q, label);
                const sub = rowAdapter.getSub ? rowAdapter.getSub(row) : undefined;
                const capturedId = id;
                rowScored.push({
                    item: {
                        id: `__row_${id}`,
                        icon,
                        label,
                        labelRanges: labelMatch ? labelMatch.ranges : [],
                        sub,
                        onSelect: () => { rowAdapter.onSelect(capturedId); onClose(); },
                    },
                    score: match.score,
                });
            }
        }

        rowScored.sort((a, b) => b.score - a.score);
        const capped = rowScored.slice(0, max);
        for (const entry of capped) {
            items.push(entry.item);
        }
    }

    useEffect(() => {
        if (open) {
            setQuery('');
            setActiveIdx(0);
            setTimeout(() => inputRef.current?.focus(), 20);
        }
    }, [open]);

    useEffect(() => {
        setActiveIdx(0);
    }, [query]);

    useEffect(() => {
        if (!open) return;
        const el = listRef.current?.querySelector<HTMLElement>(`[data-palette-idx="${activeIdx}"]`);
        el?.scrollIntoView({ block: 'nearest' });
    }, [activeIdx, open]);

    const handleKeyDown = useCallback((e: React.KeyboardEvent): void => {
        if (e.key === 'Escape') {
            onClose();
            return;
        }
        if (e.key === 'ArrowDown') {
            e.preventDefault();
            setActiveIdx((prev) => Math.min(prev + 1, items.length - 1));
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            setActiveIdx((prev) => Math.max(prev - 1, 0));
        } else if (e.key === 'Enter') {
            e.preventDefault();
            const item = items[activeIdx];
            if (item) {
                item.onSelect();
                onClose();
            }
        }
    }, [items, activeIdx, onClose]);

    if (!open) return null;

    return (
        <div
            className="cp-backdrop"
            onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}
        >
            <div className="cp-card" onKeyDown={handleKeyDown}>
                <div className="cp-input-row">
                    <span className="cp-search-icon">⌘</span>
                    <input
                        ref={inputRef}
                        className="cp-input"
                        placeholder={placeholder}
                        value={query}
                        onChange={(e) => setQuery(e.target.value)}
                        autoComplete="off"
                        spellCheck={false}
                    />
                    <kbd className="cp-esc-hint">esc</kbd>
                </div>
                {items.length > 0 && (
                    <div ref={listRef} className="cp-list">
                        {items.map((item, idx) => {
                            const showGroupHeader = item.group !== undefined &&
                                (idx === 0 || items[idx - 1].group !== item.group);
                            return (
                                <React.Fragment key={item.id}>
                                    {showGroupHeader && (
                                        <div className="cp-group-label">{item.group}</div>
                                    )}
                                    <button
                                        type="button"
                                        data-palette-idx={idx}
                                        className={`cp-item${idx === activeIdx ? ' cp-item--active' : ''}`}
                                        onMouseMove={() => { if (activeIdx !== idx) setActiveIdx(idx); }}
                                        onMouseDown={(e) => { e.preventDefault(); item.onSelect(); onClose(); }}
                                    >
                                        <span className="cp-item__icon">{item.icon}</span>
                                        <span className="cp-item__body">
                                            <span className="cp-item__label">
                                                <HighlightedLabel label={item.label} ranges={item.labelRanges} />
                                            </span>
                                            {item.sub && (
                                                <span className="cp-item__sub">{item.sub}</span>
                                            )}
                                        </span>
                                        {idx === activeIdx && (
                                            <kbd className="cp-item__enter">↵</kbd>
                                        )}
                                    </button>
                                </React.Fragment>
                            );
                        })}
                    </div>
                )}
                {items.length === 0 && q && (
                    <div className="cp-empty">No results for "{query}"</div>
                )}
            </div>
        </div>
    );
};

export default CommandPalette;
