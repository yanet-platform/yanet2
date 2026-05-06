import React, { useState, useMemo, useCallback, useEffect, lazy, Suspense } from 'react';
import { BrowserRouter, Routes, Route, useLocation, useNavigate, Navigate } from 'react-router-dom';
import MainMenu from './MainMenu';
import { PageLoader } from './components';
import type { PageId, SidebarContextValue } from './types';
import { PAGE_IDS, SidebarContext } from './types';

const importNeighbours = () => import('./pages/NeighboursPage');
const importInspect = () => import('./pages/InspectPage');
const importRoute = () => import('./pages/RoutePage');
const importFunctions = () => import('./pages/FunctionsPage');
const importPipelines = () => import('./pages/PipelinesPage');
const importDevices = () => import('./pages/DevicesPage');
const importDecap = () => import('./pages/DecapPage');
const importForward = () => import('./pages/ForwardPage');
const importPdump = () => import('./pages/PdumpPage');
const importAcl = () => import('./pages/AclPage');

const pageImporters = [
    importNeighbours,
    importInspect,
    importRoute,
    importFunctions,
    importPipelines,
    importDevices,
    importDecap,
    importForward,
    importPdump,
    importAcl,
];

const NeighboursPage = lazy(importNeighbours);
const InspectPage = lazy(importInspect);
const RoutePage = lazy(importRoute);
const FunctionsPage = lazy(importFunctions);
const PipelinesPage = lazy(importPipelines);
const DevicesPage = lazy(importDevices);
const DecapPage = lazy(importDecap);
const ForwardPage = lazy(importForward);
const PdumpPage = lazy(importPdump);
const AclPage = lazy(importAcl);

type IdleHandle = number;
type RequestIdleCallback = (cb: () => void, opts?: { timeout: number }) => IdleHandle;
type CancelIdleCallback = (id: IdleHandle) => void;

const AppContent = (): React.JSX.Element => {
    const location = useLocation();
    const navigate = useNavigate();
    const [sidebarDisabled, setSidebarDisabled] = useState(false);

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
            return 'inspect';
        }
        const segments = path.split('/').filter(Boolean);
        const firstSegment = segments[0];
        return PAGE_IDS.includes(firstSegment as PageId) ? (firstSegment as PageId) : 'inspect';
    };

    const currentPage = getCurrentPage();

    const handlePageChange = (pageId: PageId): void => {
        navigate(`/${pageId}`);
    };

    const handleSetSidebarDisabled = useCallback((disabled: boolean) => {
        setSidebarDisabled(disabled);
    }, []);

    const sidebarContextValue: SidebarContextValue = useMemo(() => ({
        setSidebarDisabled: handleSetSidebarDisabled,
    }), [handleSetSidebarDisabled]);

    return (
        <SidebarContext.Provider value={sidebarContextValue}>
            <MainMenu
                currentPage={currentPage}
                onPageChange={handlePageChange}
                disabled={sidebarDisabled}
                renderContent={() => (
                    <Suspense fallback={<PageLoader loading size="l" />}>
                        <Routes>
                            <Route path="/" element={<Navigate to="/inspect" replace />} />
                            <Route path="/neighbours" element={<NeighboursPage />} />
                            <Route path="/inspect" element={<InspectPage />} />
                            <Route path="/route" element={<RoutePage />} />
                            <Route path="/functions" element={<FunctionsPage />} />
                            <Route path="/pipelines" element={<PipelinesPage />} />
                            <Route path="/devices" element={<DevicesPage />} />
                            <Route path="/decap" element={<DecapPage />} />
                            <Route path="/forward" element={<ForwardPage />} />
                            <Route path="/pdump" element={<PdumpPage />} />
                            <Route path="/acl" element={<AclPage />} />
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
