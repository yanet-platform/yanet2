import React, { useCallback, useEffect, useRef, useState } from 'react';
import { API } from '../../../api';
import { toaster } from '../../../utils';
import { stringToIPAddress, ipAddressToString } from '../../../utils/netip';
import { parseIPAddress } from '../../../utils';
import type { Route } from '../../../api/routes';
import { bestPathReason } from './utils';
import { BestPill, SourceChip } from './cells';

export interface LookupDrawerProps {
    open: boolean;
    configName: string;
    initialQuery?: string;
    onClose: () => void;
    onShowInTable: (prefix: string) => void;
}

const EXAMPLE_IPS = ['::1', '2a02:6b8:c15::1', '87.250.250.17'];

/** Side drawer for IP route lookup — shows matched prefix and candidate routes best-first. */
const LookupDrawer: React.FC<LookupDrawerProps> = ({
    open,
    configName,
    initialQuery,
    onClose,
    onShowInTable,
}) => {
    const [query, setQuery] = useState('');
    const [loading, setLoading] = useState(false);
    const [result, setResult] = useState<{ prefix: string; routes: Route[] } | null>(null);
    const inputRef = useRef<HTMLInputElement>(null);

    useEffect(() => {
        if (open) {
            if (initialQuery) {
                setQuery(initialQuery);
                setResult(null);
            } else {
                setQuery('');
                setResult(null);
            }
            setTimeout(() => inputRef.current?.focus(), 30);
        }
    }, [open, initialQuery]);

    const performLookup = useCallback(async (ip: string): Promise<void> => {
        const trimmed = ip.trim();
        if (!trimmed) return;

        const parsed = parseIPAddress(trimmed);
        if (!parsed.ok) {
            toaster.error('lookup-invalid-ip', 'Invalid IP address format');
            return;
        }

        const ipAddr = stringToIPAddress(trimmed);
        if (!ipAddr) {
            toaster.error('lookup-invalid-ip', 'Failed to encode IP address');
            return;
        }

        setLoading(true);
        setResult(null);
        try {
            const res = await API.route.lookupRoute({
                name: configName,
                ip_addr: ipAddr,
            });
            setResult({
                prefix: res.prefix ?? '',
                routes: res.routes ?? [],
            });
        } catch (err) {
            toaster.error('lookup-error', 'Route lookup failed', err);
        } finally {
            setLoading(false);
        }
    }, [configName]);

    useEffect(() => {
        if (open && initialQuery) {
            void performLookup(initialQuery);
        }
    }, [open, initialQuery, performLookup]);

    const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLInputElement>): void => {
        if (e.key === 'Enter') {
            e.preventDefault();
            void performLookup(query);
        } else if (e.key === 'Escape') {
            onClose();
        }
    }, [query, performLookup, onClose]);

    const reasonLine = result && result.routes.length > 1
        ? bestPathReason(result.routes)
        : '';

    return (
        <>
            <div
                className={`yn-backdrop${open ? ' yn-backdrop--open' : ''}`}
                onClick={onClose}
            />
            <div className={`yn-drawer ro-lookup-drawer${open ? ' yn-drawer--open' : ''}`}>
                <div className="yn-drawer__head">
                    <h2 className="yn-drawer__title">Route Lookup</h2>
                    <button
                        type="button"
                        className="yn-icon-btn"
                        onClick={onClose}
                        aria-label="Close"
                    >
                        ✕
                    </button>
                </div>

                <div className="yn-drawer__body">
                    <div className="ro-lookup-input-row">
                        <input
                            ref={inputRef}
                            className="yn-input yn-input--mono"
                            placeholder="Enter IPv4 or IPv6 address…"
                            value={query}
                            onChange={(e) => setQuery(e.target.value)}
                            onKeyDown={handleKeyDown}
                            autoComplete="off"
                            spellCheck={false}
                        />
                        <button
                            type="button"
                            className="yn-btn yn-btn--primary"
                            onClick={() => void performLookup(query)}
                            disabled={loading || !query.trim()}
                        >
                            {loading ? '…' : 'Look up'}
                        </button>
                    </div>

                    <div className="ro-lookup-examples">
                        <span className="ro-lookup-examples__label">Examples:</span>
                        {EXAMPLE_IPS.map((ip) => (
                            <button
                                key={ip}
                                type="button"
                                className="ro-lookup-example-chip"
                                onClick={() => {
                                    setQuery(ip);
                                    void performLookup(ip);
                                }}
                            >
                                {ip}
                            </button>
                        ))}
                    </div>

                    {loading && (
                        <div className="ro-lookup-loading">Looking up…</div>
                    )}

                    {result && !loading && (
                        <div className="ro-lookup-result">
                            <div className="ro-lookup-result__matched">
                                <span className="ro-lookup-result__label">Matched prefix</span>
                                <span className="ro-lookup-result__prefix">
                                    {result.prefix || '(none)'}
                                </span>
                                {result.prefix && (
                                    <button
                                        type="button"
                                        className="ro-lookup-show-in-table"
                                        onClick={() => {
                                            onShowInTable(result.prefix);
                                            onClose();
                                        }}
                                    >
                                        Show in table →
                                    </button>
                                )}
                            </div>

                            {reasonLine && (
                                <div className="ro-lookup-reason">
                                    Best because: {reasonLine}
                                </div>
                            )}

                            {result.routes.length > 0 ? (
                                <div className="ro-lookup-table-wrap">
                                    <table className="ro-lookup-table">
                                        <thead>
                                            <tr>
                                                <th></th>
                                                <th>Prefix</th>
                                                <th>Next Hop</th>
                                                <th>Peer</th>
                                                <th>Source</th>
                                                <th>Peer AS</th>
                                                <th>Origin</th>
                                                <th>Pref</th>
                                                <th>MED</th>
                                                <th>Communities</th>
                                            </tr>
                                        </thead>
                                        <tbody>
                                            {result.routes.map((r, idx) => (
                                                <tr
                                                    key={idx}
                                                    className={idx === 0 ? 'ro-lookup-table__best-row' : ''}
                                                >
                                                    <td>
                                                        <BestPill isBest={idx === 0} />
                                                    </td>
                                                    <td className="yn-cell-mono yn-cell-strong">
                                                        {r.prefix || '—'}
                                                    </td>
                                                    <td className="yn-cell-mono yn-cell-muted">
                                                        {ipAddressToString(r.next_hop) || '—'}
                                                    </td>
                                                    <td className="yn-cell-mono yn-cell-muted">
                                                        {ipAddressToString(r.peer) || '—'}
                                                    </td>
                                                    <td>
                                                        <SourceChip source={r.source} />
                                                    </td>
                                                    <td className="yn-cell-muted">
                                                        {r.peer_as ?? '—'}
                                                    </td>
                                                    <td className="yn-cell-muted">
                                                        {r.origin_as ?? '—'}
                                                    </td>
                                                    <td className="yn-cell-muted">
                                                        {r.pref ?? '—'}
                                                    </td>
                                                    <td className="yn-cell-muted">
                                                        {r.med ?? '—'}
                                                    </td>
                                                    <td className="yn-cell-muted ro-lookup-communities">
                                                        {r.large_communities && r.large_communities.length > 0
                                                            ? r.large_communities
                                                                .map((c) => `${c.global_administrator ?? 0}:${c.local_data_part1 ?? 0}:${c.local_data_part2 ?? 0}`)
                                                                .join(' ')
                                                            : '—'}
                                                    </td>
                                                </tr>
                                            ))}
                                        </tbody>
                                    </table>
                                </div>
                            ) : (
                                <div className="ro-lookup-no-match">No matching routes found.</div>
                            )}
                        </div>
                    )}
                </div>
            </div>
        </>
    );
};

export default LookupDrawer;
