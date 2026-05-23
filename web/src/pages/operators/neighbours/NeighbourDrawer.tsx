import React, { useEffect, useState } from 'react';
import { Select } from '@gravity-ui/uikit';
import { DraftItemDrawer } from '../../_shared/draft';
import { ipAddressToString, stringToIPAddress } from '../../../utils/netip';
import type { Neighbour, NeighbourTableInfo } from '../../../api/neighbours';
import { validateMAC, validateNextHop } from './utils';
import { MERGED_TAB } from './types';

export interface NeighbourDrawerProps {
    open: boolean;
    mode: 'add' | 'edit';
    tables: NeighbourTableInfo[];
    defaultTable: string;
    neighbour: Neighbour | null;
    activeTable: string;
    onClose: () => void;
    onSubmit: (table: string, entry: Neighbour) => Promise<void>;
    onDelete?: (neighbour: Neighbour) => Promise<void>;
}

/** Drawer for adding or editing a single neighbour entry. */
const NeighbourDrawer: React.FC<NeighbourDrawerProps> = ({
    open,
    mode,
    tables,
    defaultTable,
    neighbour,
    activeTable,
    onClose,
    onSubmit,
    onDelete,
}) => {
    const isMergedAdd = mode === 'add' && activeTable === MERGED_TAB;

    const tableOptions = tables
        .filter((t) => t.name)
        .map((t) => ({ value: t.name!, content: t.name! }));

    const [selectedTable, setSelectedTable] = useState<string[]>([defaultTable]);
    const [nextHop, setNextHop] = useState('');
    const [linkAddr, setLinkAddr] = useState('');
    const [hardwareAddr, setHardwareAddr] = useState('');
    const [device, setDevice] = useState('');
    const [priority, setPriority] = useState('');
    const [submitting, setSubmitting] = useState(false);

    useEffect(() => {
        if (open) {
            setSelectedTable([defaultTable]);
            if (mode === 'edit' && neighbour) {
                setNextHop(ipAddressToString(neighbour.next_hop));
                setLinkAddr(neighbour.link_addr?.addr || '');
                setHardwareAddr(neighbour.hardware_addr?.addr || '');
                setDevice(neighbour.device || '');
                setPriority(neighbour.priority?.toString() || '');
            } else {
                setNextHop('');
                setLinkAddr('');
                setHardwareAddr('');
                setDevice('');
                setPriority('');
            }
            setSubmitting(false);
        }
    }, [open, mode, defaultTable, neighbour]);

    const nextHopError = mode === 'add' ? validateNextHop(nextHop) : undefined;
    const linkAddrError = validateMAC(linkAddr);
    const hardwareAddrError = validateMAC(hardwareAddr);

    const canSubmit =
        !submitting &&
        (mode === 'edit' || nextHop.trim() !== '') &&
        !nextHopError &&
        !linkAddrError &&
        !hardwareAddrError;

    const handleApply = async (): Promise<void> => {
        if (!canSubmit) return;
        setSubmitting(true);
        try {
            const resolvedTable = (() => {
                if (mode === 'add' && activeTable === MERGED_TAB) {
                    return selectedTable[0] || defaultTable;
                }
                if (mode === 'edit' && activeTable === MERGED_TAB) {
                    return neighbour?.source || 'static';
                }
                return activeTable;
            })();

            let nextHopWire: Neighbour['next_hop'];
            if (mode === 'add') {
                nextHopWire = stringToIPAddress(nextHop.trim()) ?? undefined;
                if (!nextHopWire) return;
            } else {
                nextHopWire = neighbour?.next_hop;
            }

            const entry: Neighbour = {
                next_hop: nextHopWire,
                device: device.trim() || undefined,
                priority: priority.trim() ? Number(priority.trim()) : undefined,
            };
            if (linkAddr.trim()) entry.link_addr = { addr: linkAddr.trim() };
            if (hardwareAddr.trim()) entry.hardware_addr = { addr: hardwareAddr.trim() };

            await onSubmit(resolvedTable, entry);
            onClose();
        } finally {
            setSubmitting(false);
        }
    };

    const handleDelete = async (): Promise<void> => {
        if (!neighbour || !onDelete) return;
        setSubmitting(true);
        try {
            await onDelete(neighbour);
            onClose();
        } finally {
            setSubmitting(false);
        }
    };

    const resolvedTableForTitle = (() => {
        if (mode === 'add' && activeTable === MERGED_TAB) {
            return selectedTable[0] || defaultTable;
        }
        if (mode === 'edit' && activeTable === MERGED_TAB) {
            return neighbour?.source || 'static';
        }
        return activeTable;
    })();
    const tableLabel = mode === 'add' && activeTable === MERGED_TAB
        ? undefined
        : resolvedTableForTitle || undefined;
    const titleSingular = tableLabel ? `neighbour in ${tableLabel}` : 'neighbour';

    return (
        <DraftItemDrawer
            open={open}
            index={0}
            total={1}
            titleSingular={titleSingular}
            titleVerb={mode === 'add' ? 'Add' : undefined}
            hideIndex={mode === 'add'}
            onClose={onClose}
            onApply={handleApply}
            onDelete={mode === 'edit' && neighbour && onDelete ? handleDelete : undefined}
            onJump={() => {}}
            ariaLabel={mode === 'add' ? 'Add neighbour' : 'Edit neighbour'}
        >
            {isMergedAdd && (
                <section className="fw-section">
                    <div className="fw-section-h">Target</div>
                    <div className="fw-section__body">
                        <div className="fw-field">
                            <label className="fw-field__label">
                                Table <span className="fw-field__req">*</span>
                            </label>
                            <Select
                                value={selectedTable}
                                onUpdate={setSelectedTable}
                                options={tableOptions}
                                width="max"
                            />
                        </div>
                    </div>
                </section>
            )}

            <section className="fw-section">
                <div className="fw-section-h">Identity</div>
                <div className="fw-section__body">
                    <div className="fw-field">
                        <label className="fw-field__label">
                            Next Hop <span className="fw-field__req">*</span>
                        </label>
                        <input
                            className={`fw-input fw-input--mono${nextHopError ? ' fw-input--invalid' : ''}`}
                            value={nextHop}
                            placeholder="192.168.1.1 or fe80::1"
                            onChange={(e) => setNextHop(e.target.value)}
                            disabled={mode === 'edit'}
                        />
                        {nextHopError && (
                            <span className="fw-field__hint fw-field__error">{nextHopError}</span>
                        )}
                    </div>
                </div>
            </section>

            <section className="fw-section">
                <div className="fw-section-h">L2</div>
                <div className="fw-section__body">
                    <div className="fw-field">
                        <label className="fw-field__label">Neighbour MAC</label>
                        <input
                            className={`fw-input fw-input--mono${linkAddrError ? ' fw-input--invalid' : ''}`}
                            value={linkAddr}
                            placeholder="52:54:00:12:34:56"
                            onChange={(e) => setLinkAddr(e.target.value)}
                        />
                        {linkAddrError && (
                            <span className="fw-field__hint fw-field__error">{linkAddrError}</span>
                        )}
                    </div>
                    <div className="fw-field">
                        <label className="fw-field__label">Interface MAC</label>
                        <input
                            className={`fw-input fw-input--mono${hardwareAddrError ? ' fw-input--invalid' : ''}`}
                            value={hardwareAddr}
                            placeholder="52:54:00:12:34:56"
                            onChange={(e) => setHardwareAddr(e.target.value)}
                        />
                        {hardwareAddrError && (
                            <span className="fw-field__hint fw-field__error">{hardwareAddrError}</span>
                        )}
                    </div>
                </div>
            </section>

            <section className="fw-section">
                <div className="fw-section-h">Egress</div>
                <div className="fw-section__body">
                    <div className="fw-field">
                        <label className="fw-field__label">Device</label>
                        <input
                            className="fw-input"
                            value={device}
                            placeholder="eth0"
                            onChange={(e) => setDevice(e.target.value)}
                        />
                    </div>
                    <div className="fw-field">
                        <label className="fw-field__label">Priority</label>
                        <input
                            className="fw-input"
                            type="number"
                            value={priority}
                            placeholder="100"
                            onChange={(e) => setPriority(e.target.value)}
                        />
                    </div>
                </div>
            </section>
        </DraftItemDrawer>
    );
};

export default NeighbourDrawer;
