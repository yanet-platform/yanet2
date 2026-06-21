import React, { useState, useMemo, useCallback, useEffect, useRef, Suspense } from 'react';
import { BrowserRouter, Routes, Route, useLocation, useNavigate, Navigate } from 'react-router-dom';
import MainMenu from './MainMenu';
import { PageLoader } from '@yanet/core/components';
import type { PageId, SidebarContextValue } from './types';
import { PAGE_IDS, SidebarContext } from './types';
import { PaletteProvider, usePalette, CommandPalette, navigationCommands, ShortcutsHelp } from '@yanet/core/components/command-palette';
import type { RowAdapter, Command } from '@yanet/core/components/command-palette';
import { GatewayProvider, GatewayDrawer, useGateways, gatewayCommands } from '@yanet/core/gateways';
import { setApiBase } from '@yanet/core/api';
import { routes, prefetchers, defaultRoute, navItems } from './registry';

type IdleHandle = number;
type RequestIdleCallback = (cb: () => void, opts?: { timeout: number }) => IdleHandle;
type CancelIdleCallback = (id: IdleHandle) => void;

/** Global command palette instance that reads contributions from context at render time. */
const GlobalPalette = ({
    handlePageChange,
    onOpenGatewayDrawer,
    onSetActiveGateway,
}: {
    handlePageChange: (id: PageId) => void;
    onOpenGatewayDrawer: () => void;
    onSetActiveGateway: (id: string) => void;
}): React.JSX.Element => {
    const { open, closePalette, contribution, helpOpen, closeHelp, helpShortcuts, openHelp } = usePalette();
    const { gateways, activeGateway } = useGateways();
    const navCmds = useMemo(() => navigationCommands(navItems, handlePageChange), [handlePageChange]);

    const showShortcutsCmd = useMemo((): Command => ({
        id: '__show_shortcuts',
        icon: '?',
        label: 'Keyboard shortcuts',
        keywords: 'help keys shortcuts hotkeys',
        group: 'Go to',
        // Defer so the palette's auto-close on select runs before the help opens.
        onSelect: () => setTimeout(openHelp, 0),
    }), [openHelp]);

    const gwCmds = useMemo(
        () => gatewayCommands(gateways, activeGateway?.id ?? null, onSetActiveGateway, onOpenGatewayDrawer),
        [gateways, activeGateway, onSetActiveGateway, onOpenGatewayDrawer],
    );

    const commands = useMemo(() => {
        const pageCmds = contribution?.commands ?? [];
        return [...pageCmds, ...navCmds, showShortcutsCmd, ...gwCmds];
    }, [contribution, gwCmds, navCmds, showShortcutsCmd]);

    const dynamicCommands = useMemo(() => {
        const pageDynamic = contribution?.dynamicCommands;
        if (!pageDynamic) return undefined;
        return pageDynamic;
    }, [contribution]);

    const rowAdapter = contribution?.rowAdapter as RowAdapter<unknown> | undefined;
    const placeholder = contribution?.placeholder ?? 'Search or jump to a page…';

    return (
        <>
            <CommandPalette<unknown>
                open={open}
                onClose={closePalette}
                placeholder={placeholder}
                commands={commands}
                dynamicCommands={dynamicCommands}
                rowAdapter={rowAdapter}
            />
            <ShortcutsHelp
                open={helpOpen}
                onClose={closeHelp}
                pageSections={helpShortcuts}
            />
        </>
    );
};

const AppContentInner = (): React.JSX.Element => {
    const location = useLocation();
    const navigate = useNavigate();
    const [sidebarDisabled, setSidebarDisabled] = useState(false);
    const [gatewayDrawerOpen, setGatewayDrawerOpen] = useState(false);
    const [asideSize, setAsideSize] = useState<number>(0);
    const unsavedGuardRef = useRef<(() => boolean) | null>(null);
    const { activeGateway, setActive } = useGateways();

    // Apply the base URL synchronously so it's already set before the keyed
    // <Routes> subtree mounts — page mount effects must not see a stale base.
    if (activeGateway) {
        setApiBase(activeGateway.baseUrl);
    }

    const handleOpenGatewayDrawer = useCallback(() => {
        setGatewayDrawerOpen(true);
    }, []);

    const handleCloseGatewayDrawer = useCallback(() => {
        setGatewayDrawerOpen(false);
    }, []);

    const handleToggleGatewayDrawer = useCallback(() => {
        setGatewayDrawerOpen((prev) => !prev);
    }, []);

    useEffect(() => {
        let cancelled = false;
        const prefetchAll = (): void => {
            if (cancelled) {
                return;
            }
            // Fire all imports; failures are non-fatal (e.g. transient network).
            prefetchers.forEach((load) => {
                load().catch(() => {});
            });
        };

        let handle: IdleHandle | null = null;
        const ric = (window as unknown as { requestIdleCallback?: RequestIdleCallback }).requestIdleCallback;
        const cic = (window as unknown as { cancelIdleCallback?: CancelIdleCallback }).cancelIdleCallback;

        if (ric) {
            handle = ric(prefetchAll, { timeout: 2000 });
        } else {
            handle = window.setTimeout(prefetchAll, 1500) as unknown as IdleHandle;
        }

        return () => {
            cancelled = true;
            if (handle !== null) {
                if (cic) {
                    cic(handle);
                } else {
                    window.clearTimeout(handle);
                }
            }
        };
    }, []);

    const getCurrentPage = (): PageId => {
        const path = location.pathname;
        if (path === '/' || path === '') {
            return 'builtin/dashboard';
        }
        const segments = path.split('/').filter(Boolean);
        if (segments.length >= 2) {
            const candidate = `${segments[0]}/${segments[1]}` as PageId;
            if ((PAGE_IDS as ReadonlyArray<string>).includes(candidate)) {
                return candidate;
            }
        }
        return 'builtin/dashboard';
    };

    const currentPage = getCurrentPage();

    const handlePageChange = useCallback((pageId: PageId): void => {
        const guard = unsavedGuardRef.current;
        if (guard && guard()) {
            const ok = window.confirm('You have unsaved changes. Leave this page anyway?');
            if (!ok) {
                return;
            }
        }
        navigate(`/${pageId}`);
    }, [navigate]);

    /** Switches the active gateway with the same unsaved-changes guard as page navigation. */
    const handleSetActiveGateway = useCallback((id: string): void => {
        const guard = unsavedGuardRef.current;
        if (guard && guard()) {
            const ok = window.confirm('You have unsaved changes. Leave this page anyway?');
            if (!ok) {
                return;
            }
        }
        setActive(id);
    }, [setActive]);

    const handleSetSidebarDisabled = useCallback((disabled: boolean) => {
        setSidebarDisabled(disabled);
    }, []);

    const setUnsavedGuard = useCallback((cb: (() => boolean) | null) => {
        unsavedGuardRef.current = cb;
    }, []);

    const sidebarContextValue: SidebarContextValue = useMemo(() => ({
        setSidebarDisabled: handleSetSidebarDisabled,
        setUnsavedGuard,
    }), [handleSetSidebarDisabled, setUnsavedGuard]);

    return (
        <SidebarContext.Provider value={sidebarContextValue}>
            <PaletteProvider>
                <GlobalPalette handlePageChange={handlePageChange} onOpenGatewayDrawer={handleOpenGatewayDrawer} onSetActiveGateway={handleSetActiveGateway} />
                <GatewayDrawer open={gatewayDrawerOpen} onClose={handleCloseGatewayDrawer} asideSize={asideSize} onSetActive={handleSetActiveGateway} />
                <MainMenu
                    currentPage={currentPage}
                    onPageChange={handlePageChange}
                    disabled={sidebarDisabled}
                    onOpenGatewayDrawer={handleOpenGatewayDrawer}
                    onToggleGatewayDrawer={handleToggleGatewayDrawer}
                    gatewayDrawerOpen={gatewayDrawerOpen}
                    onAsideSize={setAsideSize}
                    renderContent={() => (
                        <div className="app-surface">
                            <Suspense fallback={<PageLoader loading size="l" />}>
                                <Routes key={`${activeGateway?.id ?? ''}:${activeGateway?.baseUrl ?? ''}`}>
                                    <Route path="/" element={<Navigate to={defaultRoute} replace />} />
                                    {routes.flatMap(({ manifest, Component }) => [
                                        <Route key={manifest.route} path={manifest.route} element={<Component />} />,
                                        ...(manifest.redirects ?? []).map((from) => (
                                            <Route key={from} path={from} element={<Navigate to={manifest.route} replace />} />
                                        )),
                                    ])}
                                </Routes>
                            </Suspense>
                        </div>
                    )}
                />
            </PaletteProvider>
        </SidebarContext.Provider>
    );
};

const AppContent = (): React.JSX.Element => (
    <GatewayProvider>
        <AppContentInner />
    </GatewayProvider>
);

const App = (): React.JSX.Element => {
    return (
        <BrowserRouter>
            <AppContent />
        </BrowserRouter>
    );
};

export default App;
