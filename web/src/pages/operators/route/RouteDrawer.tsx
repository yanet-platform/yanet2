import React, { useEffect, useRef, useState } from 'react';
import { Switch } from '@gravity-ui/uikit';
import { DraftItemDrawer } from '../../../components/draft';
import { ipAddressToString } from '../../../utils/netip';
import { validatePrefix, validateNexthop } from './utils';
import type { Route } from '../../../api/routes';
import { CidrPrefixField } from '../../../components';

export interface RouteDrawerProps {
    open: boolean;
    mode: 'add' | 'edit';
    route: Route | null;
    configName: string;
    onClose: () => void;
    onSubmit: (params: { prefix: string; nexthopAddr: string; doFlush: boolean }) => Promise<void>;
    onDelete?: (route: Route) => Promise<void>;
}

/** Drawer for adding or editing a single RIB route. */
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
    const [nexthopAddr, setNexthopAddr] = useState('');
    const [doFlush, setDoFlush] = useState(false);
    const [submitting, setSubmitting] = useState(false);

    useEffect(() => {
        if (open) {
            setPrefix(mode === 'edit' && route ? (route.prefix || '') : '');
            setNexthopAddr(mode === 'edit' && route ? ipAddressToString(route.next_hop) : '');
            setDoFlush(false);
            setSubmitting(false);
        }
    }, [open, mode, route?.prefix, route?.next_hop]);

    const prefixError = validatePrefix(prefix);
    const nexthopError = validateNexthop(nexthopAddr);
    const canSubmit = prefix.trim() !== '' && nexthopAddr.trim() !== '' && !prefixError && !nexthopError && !submitting;

    const handleApply = async (): Promise<void> => {
        if (!canSubmit) return;
        setSubmitting(true);
        try {
            await onSubmit({ prefix: prefix.trim(), nexthopAddr: nexthopAddr.trim(), doFlush });
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
                <div className="yn-section-h">Next Hop</div>
                <div className="yn-section__body">
                    <div className="yn-field">
                        <label className="yn-field__label">
                            Next Hop IP <span className="yn-field__req">*</span>
                        </label>
                        <input
                            className={`yn-input yn-input--mono${nexthopError ? ' yn-input--invalid' : ''}`}
                            value={nexthopAddr}
                            placeholder="192.168.1.1 or 2001:db8::1"
                            onChange={(e) => setNexthopAddr(e.target.value)}
                        />
                        {nexthopError && (
                            <span className="yn-field__hint yn-field__error">{nexthopError}</span>
                        )}
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
