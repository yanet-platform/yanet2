import React, { useCallback, useEffect, useRef } from 'react';
import type { Module, Chain } from '../types';
import { metaFor } from '../moduleMeta';
import { InlineEdit } from './InlineEdit';
import { Sparkline, useSparklineHistory } from '../../_shared/lane-editor';
import { CloseIcon, TrashIcon } from '../../_shared/icons';
import { formatPps, formatBps } from '../../../../utils';
import { ConfirmDialog } from '../../../../components';
import type { InterpolatedCounterData } from '../../../../hooks';

/** Props for module-mode drawer. */
interface DrawerModuleProps {
    mode: 'module';
    module: Module;
    chain: Chain | null;
    counter?: InterpolatedCounterData;
    availableTypes: string[];
    onClose: () => void;
    onRename: (newName: string) => void;
    onChangeType: (newType: string) => void;
    onRemove: () => void;
}

/** Props for chain-mode drawer. */
interface DrawerChainProps {
    mode: 'chain';
    chain: Chain;
    fnId: string;
    onClose: () => void;
    onUpdateChain: (patch: Partial<Chain>) => void;
    onRemoveChain: () => void;
    onReorderModule: (fromIdx: number, toIdx: number) => void;
    onAddModuleToChain: (module: Module) => void;
}

type DrawerProps = DrawerModuleProps | DrawerChainProps;

/** Up arrow icon for module reorder. */
const UpIcon = (): React.JSX.Element => (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M12 19V5M5 12l7-7 7 7" />
    </svg>
);

/** Down arrow icon for module reorder. */
const DownIcon = (): React.JSX.Element => (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M12 5v14M19 12l-7 7-7-7" />
    </svg>
);

interface DrawerActionProps {
    icon: React.ReactNode;
    label: string;
    danger?: boolean;
    onClick?: () => void;
}

const DrawerAction = ({ icon, label, danger, onClick }: DrawerActionProps): React.JSX.Element => (
    <button
        className={`fn-drawer__action-btn${danger ? ' fn-drawer__action-btn--danger' : ''}`}
        type="button"
        onClick={onClick}
    >
        {icon}
        {label}
    </button>
);

interface BigStatProps {
    label: string;
    value: string;
    accent?: string;
}

const BigStat = ({ label, value, accent }: BigStatProps): React.JSX.Element => (
    <div className="fn-drawer__big-stat">
        <div className="fn-drawer__big-stat-label">{label}</div>
        <div
            className="fn-drawer__big-stat-value"
            style={accent ? { color: accent } : undefined}
        >
            {value}
        </div>
    </div>
);

/** Module-mode drawer content. */
const ModuleDrawerContent = ({
    props,
    confirmRemove,
    setConfirmRemove,
    validateName,
}: {
    props: DrawerModuleProps;
    confirmRemove: boolean;
    setConfirmRemove: (v: boolean) => void;
    validateName: (name: string) => string | null;
}): React.JSX.Element => {
    const { module, chain, counter, availableTypes, onClose, onRename, onChangeType, onRemove } = props;
    const meta = metaFor(module.type);
    const accent = meta.color;
    const sparklineData = useSparklineHistory(module.id, counter?.pps ?? 0);

    return (
        <>
            <div className="fn-drawer__header">
                <div className="fn-drawer__title">
                    <div
                        className="fn-drawer__subtitle"
                        style={{ color: accent }}
                    >
                        MODULE · {module.type.toUpperCase()}
                    </div>
                    <InlineEdit
                        value={module.name}
                        onChange={onRename}
                        validate={validateName}
                        className="fn-drawer__name-edit"
                    />
                </div>
                <button
                    className="fn-drawer__close-btn"
                    onClick={onClose}
                    type="button"
                    aria-label="Close drawer"
                >
                    <CloseIcon />
                </button>
            </div>

            <div className="fn-drawer__section">
                <div className="fn-drawer__section-label">Live counters</div>
                <div className="fn-drawer__counters-grid">
                    <BigStat label="PPS" value={counter ? formatPps(counter.pps) : '—'} accent={accent} />
                    <BigStat label="BPS" value={counter ? formatBps(counter.bps) : '—'} />
                </div>
                {sparklineData.length >= 4 && (
                    <div>
                        <div className="fn-drawer__sparkline-label">pps · last {sparklineData.length} samples</div>
                        <div className="fn-drawer__sparkline">
                            <Sparkline
                                data={sparklineData}
                                width={364}
                                height={48}
                                color={accent}
                            />
                        </div>
                    </div>
                )}
            </div>

            <div className="fn-drawer__section">
                <div className="fn-drawer__section-label">Configuration</div>
                <div className="fn-drawer__field">
                    <div className="fn-drawer__field-label">Module type</div>
                    <select
                        className="fn-drawer__type-select"
                        value={module.type}
                        onChange={e => onChangeType(e.target.value)}
                    >
                        {availableTypes.length > 0
                            ? availableTypes.map(t => (
                                <option key={t} value={t}>{t}</option>
                            ))
                            : <option value={module.type}>{module.type}</option>
                        }
                    </select>
                </div>
                <div className="fn-drawer__field">
                    <div className="fn-drawer__field-label">Instance name</div>
                    <InlineEdit
                        value={module.name}
                        onChange={onRename}
                        validate={validateName}
                    />
                </div>
                {chain && (
                    <div className="fn-drawer__field">
                        <div className="fn-drawer__field-label">Chain</div>
                        <div className="fn-drawer__chain-info">
                            {chain.name} · weight ×{chain.weight}
                        </div>
                    </div>
                )}
            </div>

            <div className="fn-drawer__section">
                <div className="fn-drawer__section-label">Actions</div>
                <div className="fn-drawer__actions" style={{ padding: 0 }}>
                    <DrawerAction
                        icon={<TrashIcon />}
                        label="Remove from chain"
                        danger
                        onClick={() => setConfirmRemove(true)}
                    />
                </div>
            </div>

            <ConfirmDialog
                open={confirmRemove}
                onClose={() => setConfirmRemove(false)}
                onConfirm={() => { setConfirmRemove(false); onRemove(); onClose(); }}
                title="Remove module"
                message={`Remove "${module.name}" from the chain? This cannot be undone.`}
                confirmText="Remove"
                cancelText="Cancel"
                danger
            />
        </>
    );
};

/** Plus icon for adding a module. */
const PlusIcon = (): React.JSX.Element => (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M12 5v14M5 12h14" />
    </svg>
);

/** Chain-mode drawer content. */
const ChainDrawerContent = ({
    props,
    confirmDelete,
    setConfirmDelete,
}: {
    props: DrawerChainProps;
    confirmDelete: boolean;
    setConfirmDelete: (v: boolean) => void;
}): React.JSX.Element => {
    const { chain, fnId, onClose, onUpdateChain, onRemoveChain, onReorderModule, onAddModuleToChain } = props;

    return (
        <>
            <div className="fn-drawer__header">
                <div className="fn-drawer__title">
                    <div className="fn-drawer__subtitle">
                        CHAIN · weight ×{chain.weight}
                    </div>
                    <InlineEdit
                        value={chain.name}
                        onChange={name => onUpdateChain({ name })}
                        className="fn-drawer__name-edit"
                    />
                </div>
                <button
                    className="fn-drawer__close-btn"
                    onClick={onClose}
                    type="button"
                    aria-label="Close drawer"
                >
                    <CloseIcon />
                </button>
            </div>

            <div className="fn-drawer__section">
                <div className="fn-drawer__section-label">Routing</div>
                <div className="fn-drawer__field">
                    <div className="fn-drawer__field-label">Chain name</div>
                    <InlineEdit
                        value={chain.name}
                        onChange={name => onUpdateChain({ name })}
                    />
                </div>
                <div className="fn-drawer__field">
                    <div className="fn-drawer__field-label">Weight (load balancing)</div>
                    <input
                        type="number"
                        className="fn-drawer__weight-input"
                        value={chain.weight}
                        min={0}
                        onChange={e => onUpdateChain({ weight: parseInt(e.target.value || '0', 10) })}
                    />
                </div>
            </div>

            <div className="fn-drawer__section">
                <div className="fn-drawer__section-label">Modules ({chain.modules.length})</div>
                {chain.modules.length === 0 ? (
                    <div className="fn-drawer__empty-chain">
                        Empty chain — packets pass through unmodified
                    </div>
                ) : (
                    <div className="fn-drawer__module-list">
                        {chain.modules.map((m, idx) => (
                            <div key={m.id} className="fn-drawer__module-row">
                                <span className="fn-drawer__module-idx">{idx + 1}</span>
                                <span
                                    className="fn-drawer__module-type-chip"
                                    style={{
                                        background: `${metaFor(m.type).color}1f`,
                                        color: metaFor(m.type).color,
                                    }}
                                >
                                    {m.type}
                                </span>
                                <span className="fn-drawer__module-name">{m.name}</span>
                                <button
                                    className="fn-drawer__reorder-btn"
                                    type="button"
                                    disabled={idx === 0}
                                    onClick={() => onReorderModule(idx, idx - 1)}
                                    aria-label="Move up"
                                >
                                    <UpIcon />
                                </button>
                                <button
                                    className="fn-drawer__reorder-btn"
                                    type="button"
                                    disabled={idx === chain.modules.length - 1}
                                    onClick={() => onReorderModule(idx, idx + 2)}
                                    aria-label="Move down"
                                >
                                    <DownIcon />
                                </button>
                            </div>
                        ))}
                    </div>
                )}
                <button
                    className="fn-drawer__add-module-btn"
                    type="button"
                    onClick={() => {
                        const existingNames = new Set(chain.modules.map(m => m.name));
                        let autoIdx = chain.modules.length;
                        let name = `route${autoIdx}`;
                        while (existingNames.has(name)) {
                            autoIdx++;
                            name = `route${autoIdx}`;
                        }
                        const newModule: Module = {
                            id: `${Date.now()}-${Math.random().toString(36).slice(2)}`,
                            type: 'route',
                            name,
                        };
                        onAddModuleToChain(newModule);
                    }}
                >
                    <PlusIcon />
                    Add module
                </button>
            </div>

            <div className="fn-drawer__section">
                <div className="fn-drawer__section-label">Actions</div>
                <div className="fn-drawer__actions" style={{ padding: 0 }}>
                    <DrawerAction
                        icon={<TrashIcon />}
                        label="Delete chain"
                        danger
                        onClick={() => setConfirmDelete(true)}
                    />
                </div>
            </div>

            <ConfirmDialog
                open={confirmDelete}
                onClose={() => setConfirmDelete(false)}
                onConfirm={() => { setConfirmDelete(false); onRemoveChain(); onClose(); }}
                title="Delete chain"
                message={`Delete chain "${chain.name}" from function "${fnId}"? This cannot be undone.`}
                confirmText="Delete"
                cancelText="Cancel"
                danger
            />
        </>
    );
};

/**
 * Slide-in right-side inspector drawer. Renders module details or chain details
 * depending on the mode prop.
 */
export const Drawer: React.FC<DrawerProps> = (props) => {
    const [confirmAction, setConfirmAction] = React.useState(false);
    const drawerRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        const handleKey = (e: KeyboardEvent): void => {
            if (e.key === 'Escape') {
                props.onClose();
            }
        };
        document.addEventListener('keydown', handleKey);
        return () => document.removeEventListener('keydown', handleKey);
    }, [props.onClose]);

    const validateName = useCallback((name: string): string | null => {
        if (props.mode !== 'module') {
            return null;
        }
        if (!name.trim()) {
            return 'Name cannot be empty';
        }
        return null;
    }, [props.mode]);

    return (
        <>
            <div className="fn-drawer__backdrop" onClick={props.onClose} />
            <div
                className="fn-drawer"
                ref={drawerRef}
                role="dialog"
                aria-label={props.mode === 'module' ? 'Module inspector' : 'Chain inspector'}
            >
                {props.mode === 'module' ? (
                    <ModuleDrawerContent
                        props={props}
                        confirmRemove={confirmAction}
                        setConfirmRemove={setConfirmAction}
                        validateName={validateName}
                    />
                ) : (
                    <ChainDrawerContent
                        props={props}
                        confirmDelete={confirmAction}
                        setConfirmDelete={setConfirmAction}
                    />
                )}
            </div>
        </>
    );
};
