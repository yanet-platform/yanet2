import React, { useState, useMemo, useCallback } from 'react';
import { BrowserRouter, Routes, Route, useLocation, useNavigate, Navigate } from 'react-router-dom';
import MainMenu from './MainMenu';
import NeighboursPage from './pages/NeighboursPage';
import InspectPage from './pages/InspectPage';
import RoutePage from './pages/RoutePage';
import FunctionsPage from './pages/FunctionsPage';
import PipelinesPage from './pages/PipelinesPage';
import DevicesPage from './pages/DevicesPage';
import DecapPage from './pages/DecapPage';
import PdumpPage from './pages/PdumpPage';
import AclPage from './pages/AclPage';
import type { PageId, SidebarContextValue } from './types';
import { PAGE_IDS, SidebarContext } from './types';

const AppContent = (): React.JSX.Element => {
    const location = useLocation();
    const navigate = useNavigate();
    const [sidebarDisabled, setSidebarDisabled] = useState(false);

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
                    <Routes>
                        <Route path="/" element={<Navigate to="/inspect" replace />} />
                        <Route path="/neighbours" element={<NeighboursPage />} />
                        <Route path="/inspect" element={<InspectPage />} />
                        <Route path="/route" element={<RoutePage />} />
                        <Route path="/functions" element={<FunctionsPage />} />
                        <Route path="/pipelines" element={<PipelinesPage />} />
                        <Route path="/devices" element={<DevicesPage />} />
                        <Route path="/decap" element={<DecapPage />} />
                        <Route path="/pdump" element={<PdumpPage />} />
                        <Route path="/acl" element={<AclPage />} />
                    </Routes>
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
