import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Icon, Switch } from '@gravity-ui/uikit';
import { Plus, TrashBin } from '@gravity-ui/icons';
import { DraftItemDrawer } from '../../../components/draft';
import { ipAddressToString } from '../../../utils/netip';
import { validatePrefix, validateNexthop } from './utils';
import type { Route } from '../../../api/routes';
import { CidrPrefixField } from '../../../components';

/** A single nexthop row with a stable identity. */
interface NexthopRow {
    id: number;
    value: string;
}

export interface RouteDrawerProps {
    open: boolean;
    mode: 'add' | 'edit';
    route: Route | null;
    configName: string;
    onClose: () => void;
    onSubmit: (params: { prefix: string; nexthopAddrs: string[]; doFlush: boolean }) => Promise<void>;
    onDelete?: (route: Route) => Promise<void>;
}

/** Drawer for adding or editing a RIB route with one or more ECMP nexthops. */
const RouteDrawer: React.FC<RouteDrawerProps> = ({
    open,
    mode,
    route,
    configName,
    onClose,
    onSubmit,
    onDelete,
}) => {
    const [prefix, setPrefix] = useState('');
    const [nexthopRows, setNexthopRows] = useState<NexthopRow[]>([{ id: 0, value: '' }]);
    const [doFlush, setDoFlush] = useState(false);
    const [submitting, setSubmitting] = useState(false);

    const nextIdRef = useRef(1);

    const makeRow = (value: string): NexthopRow => {
        const id = nextIdRef.current;
        nextIdRef.current += 1;
        return { id, value };
    };

    useEffect(() => {
        if (open) {
            setPrefix(mode === 'edit' && route ? (route.prefix || '') : '');
            setNexthopRows(
                mode === 'edit' && route
                    ? [makeRow(ipAddressToString(route.next_hop))]
                    : [makeRow('')],
            );
            setDoFlush(false);
            setSubmitting(false);
        }
    }, [open, mode, route?.prefix, route?.next_hop]);

    const nexthopErrors = nexthopRows.map((row) => validateNexthop(row.value));
    const prefixError = validatePrefix(prefix);

    const allFilled = nexthopRows.length > 0
        && nexthopRows.every((row) => row.value.trim() !== '')
        && nexthopErrors.every((e) => !e);

    const canSubmit = prefix.trim() !== ''
        && !prefixError
        && allFilled
        && !submitting;

    const handleChangeRow = useCallback((idx: number, value: string): void => {
        setNexthopRows((prev) => prev.map((row, i) => (i === idx ? { ...row, value } : row)));
    }, []);

    const handleAddRow = useCallback((): void => {
        setNexthopRows((prev) => [...prev, makeRow('')]);
    }, []);

    const handleRemoveRow = useCallback((idx: number): void => {
        setNexthopRows((prev) => prev.length > 1 ? prev.filter((_, i) => i !== idx) : prev);
    }, []);

    const handleApply = async (): Promise<void> => {
        if (!canSubmit) return;
        setSubmitting(true);
        try {
            await onSubmit({
                prefix: prefix.trim(),
                nexthopAddrs: nexthopRows.map((row) => row.value.trim()),
                doFlush,
            });
            onClose();
        } finally {
            setSubmitting(false);
        }
    };

    const handleDelete = async (): Promise<void> => {
        if (!route || !onDelete) return;
        setSubmitting(true);
        try {
            await onDelete(route);
            onClose();
        } finally {
            setSubmitting(false);
        }
    };

    // Keep a ref so the keydown handler always sees the latest canSubmit/handleApply
    // without re-registering on every render (react-compiler safe).
    const submitRef = useRef({ canSubmit, apply: handleApply });
    submitRef.current = { canSubmit, apply: handleApply };

    useEffect(() => {
        if (!open) return;
        const handler = (e: KeyboardEvent): void => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
                e.preventDefault();
                if (submitRef.current.canSubmit) {
                    void submitRef.current.apply();
                }
            }
        };
        window.addEventListener('keydown', handler);
        return () => window.removeEventListener('keydown', handler);
    }, [open]);

    const title = mode === 'add' ? 'Add route' : 'Edit route';

    return (
        <DraftItemDrawer
            open={open}
            index={0}
            total={1}
            titleSingular={configName ? `route in ${configName}` : 'route'}
            titleVerb={mode === 'add' ? 'Add' : undefined}
            hideIndex={mode === 'add'}
            onClose={onClose}
            onApply={handleApply}
            onDelete={mode === 'edit' && route && onDelete ? handleDelete : undefined}
            onJump={() => {}}
            ariaLabel={title}
        >
            <section className="yn-section">
                <div className="yn-section-h">Destination</div>
                <div className="yn-section__body">
                    <CidrPrefixField
                        label="Prefix"
                        placeholder="10.0.0.0/8 or 2001:db8::/32"
                        value={prefix}
                        error={prefixError}
                        onChange={(v) => setPrefix(v)}
                    />
                </div>
            </section>

            <section className="yn-section">
                <div className="yn-section-h">
                    Next Hops
                    <span className="ro-nexthop-section-hint">ECMP — one per row</span>
                </div>
                <div className="yn-section__body">
                    <div className="ro-nexthop-list">
                        {nexthopRows.map((row, idx) => (
                            <div key={row.id} className="ro-nexthop-row">
                                <div className="yn-field ro-nexthop-row__field">
                                    <label className="yn-field__label">
                                        Next Hop IP <span className="yn-field__req">*</span>
                                    </label>
                                    <input
                                        className={`yn-input yn-input--mono${nexthopErrors[idx] ? ' yn-input--invalid' : ''}`}
                                        value={row.value}
                                        placeholder="192.168.1.1 or 2001:db8::1"
                                        onChange={(e) => handleChangeRow(idx, e.target.value)}
                                    />
                                    {nexthopErrors[idx] && (
                                        <span className="yn-field__hint yn-field__error">{nexthopErrors[idx]}</span>
                                    )}
                                </div>
                                {nexthopRows.length > 1 && (
                                    <button
                                        type="button"
                                        className="ro-nexthop-row__remove"
                                        title="Remove this nexthop"
                                        onClick={() => handleRemoveRow(idx)}
                                    >
                                        <Icon data={TrashBin} size={14} />
                                    </button>
                                )}
                            </div>
                        ))}
                    </div>
                    <div className="ro-nexthop-add-row">
                        <Button view="outlined" size="s" onClick={handleAddRow}>
                            <Icon data={Plus} size={14} />
                            Add nexthop
                        </Button>
                    </div>
                </div>
            </section>

            <section className="yn-section">
                <div className="yn-section-h">Apply</div>
                <div className="yn-section__body">
                    <div className="yn-field">
                        <Switch
                            checked={doFlush}
                            onUpdate={setDoFlush}
                            content="Flush RIB to FIB after this operation"
                        />
                    </div>
                </div>
            </section>
        </DraftItemDrawer>
    );
};

export default RouteDrawer;
