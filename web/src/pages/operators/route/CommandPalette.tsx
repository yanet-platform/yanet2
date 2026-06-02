import React, { useCallback, useEffect, useRef, useState } from 'react';
import { parseIPAddress } from '../../../utils';
import type { Route } from '../../../api/routes';
import { ipAddressToString } from '../../../utils/netip';
import { getRouteId } from './utils';
import type { IPFamily } from './types';

export interface PaletteAction {
    id: string;
    label: string;
    description?: string;
    onSelect: () => void;
}

export interface CommandPaletteProps {
    open: boolean;
    onClose: () => void;
    rows: Route[];
    onLookupIP: (ip: string) => void;
    onAddRoute: () => void;
    onFlush: () => void;
    onOpenLookup: () => void;
    onSetFamily: (f: IPFamily) => void;
    onToggleBestOnly: () => void;
    onToggleConflicts: () => void;
    onClearFilters: () => void;
    onJumpToRow: (id: string) => void;
}

interface PaletteItem {
    id: string;
    icon: string;
    label: string;
    sub?: string;
    onSelect: () => void;
}

const MAX_ROW_RESULTS = 7;

/** Route-scoped ⌘K command palette. */
const CommandPalette: React.FC<CommandPaletteProps> = ({
    open,
    onClose,
    rows,
    onLookupIP,
    onAddRoute,
    onFlush,
    onOpenLookup,
    onSetFamily,
    onToggleBestOnly,
    onToggleConflicts,
    onClearFilters,
    onJumpToRow,
}) => {
    const [query, setQuery] = useState('');
    const [activeIdx, setActiveIdx] = useState(0);
    const inputRef = useRef<HTMLInputElement>(null);
    const listRef = useRef<HTMLDivElement>(null);

    const isIPQuery = query.trim() !== '' && parseIPAddress(query.trim()).ok;

    const items: PaletteItem[] = [];

    if (isIPQuery) {
        const ip = query.trim();
        items.push({
            id: '__lookup_ip',
            icon: '⌖',
            label: `Look up ${ip}`,
            sub: 'Route Lookup — longest prefix match',
            onSelect: () => { onLookupIP(ip); onClose(); },
        });
    }

    if (!query.trim() || 'add route'.includes(query.toLowerCase())) {
        items.push({
            id: '__add',
            icon: '+',
            label: 'Add route',
            sub: 'Open the add-route drawer',
            onSelect: () => { onAddRoute(); onClose(); },
        });
    }
    if (!query.trim() || 'flush rib fib'.includes(query.toLowerCase())) {
        items.push({
            id: '__flush',
            icon: '⟳',
            label: 'Flush RIB → FIB',
            sub: 'Push best routes to the dataplane',
            onSelect: () => { onFlush(); onClose(); },
        });
    }
    if (!query.trim() || 'route lookup'.includes(query.toLowerCase())) {
        items.push({
            id: '__open_lookup',
            icon: '⌖',
            label: 'Open Route Lookup',
            sub: 'Look up an IP address in the RIB',
            onSelect: () => { onOpenLookup(); onClose(); },
        });
    }
    if (!query.trim() || 'best paths only'.includes(query.toLowerCase())) {
        items.push({
            id: '__best_only',
            icon: '★',
            label: 'Show best paths only',
            onSelect: () => { onToggleBestOnly(); onClose(); },
        });
    }
    if (!query.trim() || 'conflicts'.includes(query.toLowerCase())) {
        items.push({
            id: '__conflicts',
            icon: '⚡',
            label: 'Show conflicts only',
            sub: 'Prefixes with more than one candidate route',
            onSelect: () => { onToggleConflicts(); onClose(); },
        });
    }
    if (!query.trim() || 'ipv6 only'.includes(query.toLowerCase())) {
        items.push({
            id: '__v6',
            icon: '6',
            label: 'Filter IPv6 only',
            onSelect: () => { onSetFamily('v6'); onClose(); },
        });
    }
    if (!query.trim() || 'ipv4 only'.includes(query.toLowerCase())) {
        items.push({
            id: '__v4',
            icon: '4',
            label: 'Filter IPv4 only',
            onSelect: () => { onSetFamily('v4'); onClose(); },
        });
    }
    if (!query.trim() || 'clear filters'.includes(query.toLowerCase())) {
        items.push({
            id: '__clear',
            icon: '✕',
            label: 'Clear filters',
            onSelect: () => { onClearFilters(); onClose(); },
        });
    }

    if (query.trim() && !isIPQuery) {
        const q = query.trim().toLowerCase();
        let count = 0;
        for (let idx = 0; idx < rows.length && count < MAX_ROW_RESULTS; idx++) {
            const r = rows[idx];
            const prefix = r.prefix || '';
            const nh = ipAddressToString(r.next_hop);
            if (prefix.toLowerCase().includes(q) || nh.toLowerCase().includes(q)) {
                const id = getRouteId(r);
                items.push({
                    id: `__row_${id}`,
                    icon: '→',
                    label: prefix || '(no prefix)',
                    sub: nh || '—',
                    onSelect: () => { onJumpToRow(id); onClose(); },
                });
                count++;
            }
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
        const el = listRef.current?.children[activeIdx] as HTMLElement | undefined;
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
            items[activeIdx]?.onSelect();
        }
    }, [items, activeIdx, onClose]);

    if (!open) return null;

    return (
        <div
            className="ro-palette-backdrop"
            onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}
        >
            <div className="ro-palette-card" onKeyDown={handleKeyDown}>
                <div className="ro-palette-input-row">
                    <span className="ro-palette-search-icon">⌘</span>
                    <input
                        ref={inputRef}
                        className="ro-palette-input"
                        placeholder="Search routes, prefixes, or type an IP…"
                        value={query}
                        onChange={(e) => setQuery(e.target.value)}
                        autoComplete="off"
                        spellCheck={false}
                    />
                    <kbd className="ro-palette-esc-hint">esc</kbd>
                </div>
                {items.length > 0 && (
                    <div ref={listRef} className="ro-palette-list">
                        {items.map((item, idx) => (
                            <button
                                key={item.id}
                                type="button"
                                className={`ro-palette-item${idx === activeIdx ? ' ro-palette-item--active' : ''}`}
                                onMouseEnter={() => setActiveIdx(idx)}
                                onMouseDown={(e) => { e.preventDefault(); item.onSelect(); }}
                            >
                                <span className="ro-palette-item__icon">{item.icon}</span>
                                <span className="ro-palette-item__body">
                                    <span className="ro-palette-item__label">{item.label}</span>
                                    {item.sub && (
                                        <span className="ro-palette-item__sub">{item.sub}</span>
                                    )}
                                </span>
                                {idx === activeIdx && (
                                    <kbd className="ro-palette-item__enter">↵</kbd>
                                )}
                            </button>
                        ))}
                    </div>
                )}
                {items.length === 0 && query.trim() && (
                    <div className="ro-palette-empty">No results for "{query}"</div>
                )}
            </div>
        </div>
    );
};

export default CommandPalette;
