import React, { useState, useMemo, useCallback, useEffect, useRef, lazy, Suspense } from 'react';
import { BrowserRouter, Routes, Route, useLocation, useNavigate, Navigate } from 'react-router-dom';
import MainMenu from './MainMenu';
import { PageLoader } from './components';
import type { PageId, SidebarContextValue } from './types';
import { PAGE_IDS, SidebarContext } from './types';

const importInspect = () => import('./pages/builtin/inspect/InspectPage');
const importDashboard = () => import('./pages/builtin/dashboard/DashboardPage');
const importFunctions = () => import('./pages/builtin/functions/FunctionsPage');
const importPipelines = () => import('./pages/builtin/pipelines/PipelinesPage');
const importDevices = () => import('./pages/builtin/devices/DevicesPage');
const importForward = () => import('./pages/modules/forward/ForwardPage');
const importDecap = () => import('./pages/modules/decap/DecapPage');
const importAcl = () => import('./pages/modules/acl/AclPage');
const importFwState = () => import('./pages/modules/fwstate/FWStatePage');
const importPdump = () => import('./pages/modules/pdump/PdumpPage');
const importModulesRoute = () => import('./pages/modules/route/RoutePage');
const importOperatorsRoute = () => import('./pages/operators/route/RoutePage');
const importNeighbours = () => import('./pages/operators/neighbours/NeighboursPage');

const pageImporters = [
    importInspect,
    importDashboard,
    importFunctions,
    importPipelines,
    importDevices,
    importForward,
    importDecap,
    importAcl,
    importFwState,
    importPdump,
    importModulesRoute,
    importOperatorsRoute,
    importNeighbours,
];

const InspectPage = lazy(importInspect);
const DashboardPage = lazy(importDashboard);
const FunctionsPage = lazy(importFunctions);
const PipelinesPage = lazy(importPipelines);
const DevicesPage = lazy(importDevices);
const ForwardPage = lazy(importForward);
const DecapPage = lazy(importDecap);
const AclPage = lazy(importAcl);
const FWStatePage = lazy(importFwState);
const PdumpPage = lazy(importPdump);
const ModulesRoutePage = lazy(importModulesRoute);
const OperatorsRoutePage = lazy(importOperatorsRoute);
const NeighboursPage = lazy(importNeighbours);

type IdleHandle = number;
type RequestIdleCallback = (cb: () => void, opts?: { timeout: number }) => IdleHandle;
type CancelIdleCallback = (id: IdleHandle) => void;

const AppContent = (): React.JSX.Element => {
    const location = useLocation();
    const navigate = useNavigate();
    const [sidebarDisabled, setSidebarDisabled] = useState(false);
    const unsavedGuardRef = useRef<(() => boolean) | null>(null);

    useEffect(() => {
        let cancelled = false;
        const prefetchAll = (): void => {
            if (cancelled) {
                return;
            }
            // Fire all imports; failures are non-fatal (e.g. transient network).
            pageImporters.forEach((fn) => {
                fn().catch(() => {});
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

    const handlePageChange = (pageId: PageId): void => {
        const guard = unsavedGuardRef.current;
        if (guard && guard()) {
            const ok = window.confirm('You have unsaved changes. Leave this page anyway?');
            if (!ok) {
                return;
            }
        }
        navigate(`/${pageId}`);
    };

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
            <MainMenu
                currentPage={currentPage}
                onPageChange={handlePageChange}
                disabled={sidebarDisabled}
                renderContent={() => (
                    <Suspense fallback={<PageLoader loading size="l" />}>
                        <Routes>
                            <Route path="/" element={<Navigate to="/builtin/dashboard" replace />} />
                            <Route path="/builtin/inspect" element={<InspectPage />} />
                            <Route path="/builtin/dashboard" element={<DashboardPage />} />
                            <Route path="/builtin/functions" element={<FunctionsPage />} />
                            <Route path="/builtin/functions-ng" element={<Navigate to="/builtin/functions" replace />} />
                            <Route path="/builtin/pipelines" element={<PipelinesPage />} />
                            <Route path="/builtin/devices" element={<DevicesPage />} />
                            <Route path="/modules/forward" element={<ForwardPage />} />
                            <Route path="/modules/decap" element={<DecapPage />} />
                            <Route path="/modules/acl" element={<AclPage />} />
                            <Route path="/modules/fwstate" element={<FWStatePage />} />
                            <Route path="/modules/pdump" element={<PdumpPage />} />
                            <Route path="/modules/route" element={<ModulesRoutePage />} />
                            <Route path="/operators/route" element={<OperatorsRoutePage />} />
                            <Route path="/operators/neighbours" element={<NeighboursPage />} />
                            <Route path="/inspect" element={<Navigate to="/builtin/inspect" replace />} />
                            <Route path="/dashboard" element={<Navigate to="/builtin/dashboard" replace />} />
                            <Route path="/functions" element={<Navigate to="/builtin/functions" replace />} />
                            <Route path="/pipelines" element={<Navigate to="/builtin/pipelines" replace />} />
                            <Route path="/devices" element={<Navigate to="/builtin/devices" replace />} />
                            <Route path="/forward" element={<Navigate to="/modules/forward" replace />} />
                            <Route path="/decap" element={<Navigate to="/modules/decap" replace />} />
                            <Route path="/acl" element={<Navigate to="/modules/acl" replace />} />
                            <Route path="/fwstate" element={<Navigate to="/modules/fwstate" replace />} />
                            <Route path="/pdump" element={<Navigate to="/modules/pdump" replace />} />
                            <Route path="/route" element={<Navigate to="/operators/route" replace />} />
                            <Route path="/neighbours" element={<Navigate to="/operators/neighbours" replace />} />
                        </Routes>
                    </Suspense>
                )}
            />
        </SidebarContext.Provider>
    );
};

const App = (): React.JSX.Element => {
    return (
        <BrowserRouter>
            <AppContent />
        </BrowserRouter>
    );
};

export default App;
