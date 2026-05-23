import React, { useEffect, useState } from 'react';
import { Switch } from '@gravity-ui/uikit';
import { DraftItemDrawer } from '../../_shared/draft';
import { ipAddressToString } from '../../../utils/netip';
import { validatePrefix, validateNexthop } from './utils';
import type { Route } from '../../../api/routes';

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
            <section className="fw-section">
                <div className="fw-section-h">Destination</div>
                <div className="fw-section__body">
                    <div className="fw-field">
                        <label className="fw-field__label">
                            Prefix <span className="fw-field__req">*</span>
                        </label>
                        <input
                            className={`fw-input fw-input--mono${prefixError ? ' fw-input--invalid' : ''}`}
                            value={prefix}
                            placeholder="10.0.0.0/8 or 2001:db8::/32"
                            onChange={(e) => setPrefix(e.target.value)}
                        />
                        {prefixError
                            ? <span className="fw-field__hint fw-field__error">{prefixError}</span>
                            : <span className="fw-field__hint">IPv4 or IPv6 with mask.</span>
                        }
                    </div>
                </div>
            </section>

            <section className="fw-section">
                <div className="fw-section-h">Next Hop</div>
                <div className="fw-section__body">
                    <div className="fw-field">
                        <label className="fw-field__label">
                            Next Hop IP <span className="fw-field__req">*</span>
                        </label>
                        <input
                            className={`fw-input fw-input--mono${nexthopError ? ' fw-input--invalid' : ''}`}
                            value={nexthopAddr}
                            placeholder="192.168.1.1 or 2001:db8::1"
                            onChange={(e) => setNexthopAddr(e.target.value)}
                        />
                        {nexthopError && (
                            <span className="fw-field__hint fw-field__error">{nexthopError}</span>
                        )}
                    </div>
                </div>
            </section>

            <section className="fw-section">
                <div className="fw-section-h">Apply</div>
                <div className="fw-section__body">
                    <div className="fw-field">
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
