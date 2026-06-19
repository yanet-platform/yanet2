import React, { useState, useCallback, useEffect } from 'react';
import { useGateways } from './GatewayContext';
import type { Gateway, GatewayStatus } from './types';
import './GatewayDrawer.scss';

/** Derives a base URL from a raw address string entered by the user. */
export const deriveBaseUrl = (addr: string): string => {
    const trimmed = addr.trim();
    if (!trimmed) {
        return '';
    }
    if (trimmed.startsWith('http://') || trimmed.startsWith('https://')) {
        return trimmed;
    }
    return `http://${trimmed}`;
};

const statusColor = (status: GatewayStatus): string => {
    switch (status) {
        case 'online': return 'var(--gw-status-online)';
        case 'degraded': return 'var(--gw-status-degraded)';
        case 'checking': return 'var(--gw-status-checking)';
        default: return 'var(--gw-status-offline)';
    }
};

interface GatewayFormState {
    host: string;
    numa: string;
    addr: string;
}

const emptyForm = (): GatewayFormState => ({ host: '', numa: '0', addr: '' });

interface GatewayFormProps {
    title: string;
    saveLabel: string;
    initial: GatewayFormState;
    onSave: (values: GatewayFormState) => void;
    onCancel: () => void;
}

const GatewayForm: React.FC<GatewayFormProps> = ({ title, saveLabel, initial, onSave, onCancel }) => {
    const [values, setValues] = useState<GatewayFormState>(initial);

    const set = (field: keyof GatewayFormState) => (e: React.ChangeEvent<HTMLInputElement>) =>
        setValues((prev) => ({ ...prev, [field]: e.target.value }));

    const handleSave = () => {
        onSave(values);
    };

    return (
        <div
            className="gw-form"
            onClick={(e) => e.stopPropagation()}
            onKeyDown={(e) => e.stopPropagation()}
        >
            <div className="gw-form__title">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--yn-accent)" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
                    <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
                </svg>
                <span>{title}</span>
            </div>
            <div className="gw-form__row">
                <label className="gw-form__field gw-form__field--grow">
                    <span className="gw-form__label">Host / name</span>
                    <input
                        className="yn-input yn-input--mono"
                        value={values.host}
                        onChange={set('host')}
                        placeholder="gateway-01"
                    />
                </label>
                <label className="gw-form__field gw-form__field--numa">
                    <span className="gw-form__label">NUMA</span>
                    <input
                        className="yn-input yn-input--mono"
                        type="number"
                        min={0}
                        value={values.numa}
                        onChange={set('numa')}
                        placeholder="0"
                    />
                </label>
            </div>
            <label className="gw-form__field">
                <span className="gw-form__label">API address</span>
                <input
                    className="yn-input yn-input--mono"
                    value={values.addr}
                    onChange={set('addr')}
                    placeholder="10.0.0.10:8080"
                />
            </label>
            <div className="gw-form__actions">
                <button className="yn-btn yn-btn--ghost" onClick={onCancel}>Cancel</button>
                <button className="yn-btn yn-btn--primary" onClick={handleSave}>{saveLabel}</button>
            </div>
        </div>
    );
};

interface GatewayCardProps {
    gateway: Gateway;
    isActive: boolean;
    editingId: string | null;
    onSetActive: (id: string) => void;
    onStartEdit: (gw: Gateway) => void;
    onDelete: (id: string) => void;
    onSaveEdit: (id: string, values: GatewayFormState) => void;
    onCancelEdit: () => void;
}

const GatewayCard: React.FC<GatewayCardProps> = ({
    gateway,
    isActive,
    editingId,
    onSetActive,
    onStartEdit,
    onDelete,
    onSaveEdit,
    onCancelEdit,
}) => {
    const isEditing = editingId === gateway.id;
    const dotColor = statusColor(gateway.status);
    const isActivatable = !isActive && gateway.status !== 'offline';

    const handleCardClick = () => {
        if (isActivatable) {
            onSetActive(gateway.id);
        }
    };

    const handleCardKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
        if (isActivatable && (e.key === 'Enter' || e.key === ' ')) {
            e.preventDefault();
            onSetActive(gateway.id);
        }
    };

    const handleEditClick = (e: React.MouseEvent) => {
        e.stopPropagation();
        onStartEdit(gateway);
    };

    const handleDeleteClick = (e: React.MouseEvent) => {
        e.stopPropagation();
        onDelete(gateway.id);
    };

    const handleEditKeyDown = (e: React.KeyboardEvent) => {
        e.stopPropagation();
    };

    const handleDeleteKeyDown = (e: React.KeyboardEvent) => {
        e.stopPropagation();
    };

    const cardClass = [
        'gw-card',
        isActive ? 'gw-card--active' : '',
        isActivatable ? 'gw-card--activatable' : '',
        gateway.status === 'offline' ? 'gw-card--offline' : '',
    ].filter(Boolean).join(' ');

    return (
        <div
            className={cardClass}
            onClick={handleCardClick}
            onKeyDown={handleCardKeyDown}
            role={isActivatable ? 'button' : undefined}
            tabIndex={isActivatable ? 0 : undefined}
            aria-pressed={isActivatable ? isActive : undefined}
        >
            {isEditing ? (
                <GatewayForm
                    title="Edit gateway"
                    saveLabel="Save"
                    initial={{ host: gateway.host, numa: String(gateway.numa), addr: gateway.addr }}
                    onSave={(vals) => onSaveEdit(gateway.id, vals)}
                    onCancel={onCancelEdit}
                />
            ) : (
                <>
                    <div className="gw-card__header">
                        <span
                            className={`gw-dot${gateway.status === 'checking' ? ' gw-dot--pulse' : ''}`}
                            style={{ background: dotColor, boxShadow: `0 0 9px ${dotColor}70` }}
                        />
                        <div className="gw-card__info">
                            <div className="gw-card__title-row">
                                <span className="gw-card__numa">NUMA {gateway.numa}</span>
                                <span className="gw-card__status" style={{ color: dotColor }}>
                                    {gateway.status}
                                </span>
                                <span className={`gw-badge gw-badge--active${isActive ? '' : ' gw-badge--hidden'}`}>
                                    Active
                                </span>
                            </div>
                            <div className="gw-card__addr">{gateway.addr}</div>
                        </div>
                        {!gateway.builtin && (
                            <div className="gw-card__actions">
                                <button
                                    className="yn-icon-btn"
                                    onClick={handleEditClick}
                                    onKeyDown={handleEditKeyDown}
                                    aria-label="Edit"
                                >
                                    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
                                        <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
                                        <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
                                    </svg>
                                </button>
                                <button
                                    className="yn-icon-btn yn-icon-btn--danger"
                                    onClick={handleDeleteClick}
                                    onKeyDown={handleDeleteKeyDown}
                                    aria-label="Delete"
                                >
                                    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
                                        <polyline points="3 6 5 6 21 6" />
                                        <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                                    </svg>
                                </button>
                            </div>
                        )}
                    </div>
                </>
            )}
        </div>
    );
};

interface GatewayGroup {
    host: string;
    nodes: Gateway[];
}

const groupByHost = (gateways: Gateway[]): GatewayGroup[] => {
    const order: string[] = [];
    const byHost: Record<string, Gateway[]> = {};
    for (const gw of gateways) {
        if (!byHost[gw.host]) {
            byHost[gw.host] = [];
            order.push(gw.host);
        }
        byHost[gw.host].push(gw);
    }
    return order.map((host) => ({ host, nodes: byHost[host] }));
};

interface GatewayDrawerProps {
    open: boolean;
    onClose: () => void;
    /** Measured sidebar pixel width used to position the drawer flush to the sidebar edge. */
    asideSize?: number;
}

/** Full-overlay drawer listing all gateway nodes, grouped by host. */
export const GatewayDrawer: React.FC<GatewayDrawerProps> = ({ open, onClose, asideSize }) => {
    const { gateways, activeGateway, addGateway, updateGateway, deleteGateway, setActive } = useGateways();
    const [isAdding, setIsAdding] = useState(false);
    const [editingId, setEditingId] = useState<string | null>(null);

    const handleClose = useCallback(() => {
        setIsAdding(false);
        setEditingId(null);
        onClose();
    }, [onClose]);

    useEffect(() => {
        if (!open) {
            return;
        }
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                handleClose();
            }
        };
        document.addEventListener('keydown', onKeyDown);
        return () => document.removeEventListener('keydown', onKeyDown);
    }, [open, handleClose]);

    const handleStartAdd = useCallback(() => {
        setEditingId(null);
        setIsAdding(true);
    }, []);

    const handleCancelForm = useCallback(() => {
        setIsAdding(false);
        setEditingId(null);
    }, []);

    const handleSaveAdd = useCallback((vals: GatewayFormState) => {
        const host = vals.host.trim() || 'unnamed';
        const numa = parseInt(vals.numa, 10) || 0;
        const addr = vals.addr.trim() || '0.0.0.0:8080';
        addGateway({
            host,
            numa,
            addr,
            baseUrl: deriveBaseUrl(addr),
            status: 'online',
        });
        setIsAdding(false);
    }, [addGateway]);

    const handleSaveEdit = useCallback((id: string, vals: GatewayFormState) => {
        const host = vals.host.trim() || 'unnamed';
        const numa = parseInt(vals.numa, 10) || 0;
        const addr = vals.addr.trim() || '0.0.0.0:8080';
        updateGateway(id, { host, numa, addr, baseUrl: deriveBaseUrl(addr) });
        setEditingId(null);
    }, [updateGateway]);

    const handleStartEdit = useCallback((gw: Gateway) => {
        setIsAdding(false);
        setEditingId(gw.id);
    }, []);

    const handleDelete = useCallback((id: string) => {
        deleteGateway(id);
    }, [deleteGateway]);

    const handleSetActive = useCallback((id: string) => {
        setActive(id);
    }, [setActive]);

    const onlineCount = gateways.filter((g) => g.status === 'online').length;
    const degradedCount = gateways.filter((g) => g.status === 'degraded').length;
    const offlineCount = gateways.filter((g) => g.status === 'offline').length;
    const groups = groupByHost(gateways);

    const positionStyle = asideSize && asideSize > 0
        ? { left: `${asideSize}px`, maxWidth: `calc(100vw - ${asideSize}px)` }
        : undefined;

    const backdropStyle = asideSize && asideSize > 0
        ? { left: `${asideSize}px` }
        : undefined;

    return (
        <>
            <div
                className={`gw-backdrop${open ? ' gw-backdrop--open' : ''}`}
                style={backdropStyle}
                onClick={handleClose}
                aria-hidden="true"
            />
            <section
                className={`gw-drawer${open ? ' gw-drawer--open' : ''}`}
                style={positionStyle}
                role="dialog"
                aria-modal="true"
                aria-label="Gateways"
            >
                <div className="gw-drawer__header">
                    <div className="gw-drawer__header-top">
                        <div>
                            <h2 className="gw-drawer__title">Gateways</h2>
                            <p className="gw-drawer__subtitle">
                                Control-plane endpoints — one per NUMA node. Each gateway owns a single NUMA; pick which one this UI talks to.
                            </p>
                        </div>
                        <button className="gw-close-btn" onClick={handleClose} aria-label="Close">
                            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                                <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
                            </svg>
                        </button>
                    </div>
                    <div className="gw-drawer__header-bottom">
                        <div className="gw-legend">
                            <span className="gw-legend__item">
                                <span className="gw-dot gw-dot--sm" style={{ background: 'var(--gw-status-online)' }} />
                                {onlineCount} online
                            </span>
                            <span className="gw-legend__item">
                                <span className="gw-dot gw-dot--sm" style={{ background: 'var(--gw-status-degraded)' }} />
                                {degradedCount} degraded
                            </span>
                            <span className="gw-legend__item">
                                <span className="gw-dot gw-dot--sm" style={{ background: 'var(--gw-status-offline)' }} />
                                {offlineCount} offline
                            </span>
                        </div>
                        <button className="gw-btn--add" onClick={handleStartAdd}>
                            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
                                <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
                            </svg>
                            Add gateway
                        </button>
                    </div>
                </div>

                <div className="gw-drawer__body">
                    {isAdding && (
                        <div className="gw-add-form-wrapper">
                            <GatewayForm
                                title="New gateway"
                                saveLabel="Add gateway"
                                initial={emptyForm()}
                                onSave={handleSaveAdd}
                                onCancel={handleCancelForm}
                            />
                        </div>
                    )}

                    {groups.map((group) => (
                        <div key={group.host} className="gw-group">
                            <div className="gw-group__header">
                                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--yn-text-3)" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
                                    <rect x="2" y="2" width="20" height="8" rx="2" />
                                    <rect x="2" y="14" width="20" height="8" rx="2" />
                                    <line x1="6" y1="6" x2="6.01" y2="6" /><line x1="6" y1="18" x2="6.01" y2="18" />
                                </svg>
                                <span className="gw-group__host">{group.host.toUpperCase()}</span>
                                <span className="gw-group__count">{group.nodes.length} NUMA</span>
                                <div className="gw-group__line" />
                            </div>
                            <div className="gw-group__nodes">
                                {group.nodes.map((gw) => (
                                    <GatewayCard
                                        key={gw.id}
                                        gateway={gw}
                                        isActive={gw.id === activeGateway?.id}
                                        editingId={editingId}
                                        onSetActive={handleSetActive}
                                        onStartEdit={handleStartEdit}
                                        onDelete={handleDelete}
                                        onSaveEdit={handleSaveEdit}
                                        onCancelEdit={handleCancelForm}
                                    />
                                ))}
                            </div>
                        </div>
                    ))}

                    <div className="gw-config-note">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--yn-text-3)" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, marginTop: 1 }}>
                            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                            <polyline points="14 2 14 8 20 8" />
                        </svg>
                        <span>
                            Stored in this browser. Edits here are saved locally.
                        </span>
                    </div>
                </div>
            </section>
        </>
    );
};
