import React from 'react';
import { BrowserRouter, Routes, Route, useLocation, useNavigate, Navigate } from 'react-router-dom';
import MainMenu from './MainMenu';
import NeighboursPage from './pages/NeighboursPage';
import InspectPage from './pages/InspectPage';
import RoutePage from './pages/RoutePage';
import FunctionsPage from './pages/FunctionsPage';
import PipelinesPage from './pages/PipelinesPage';
import type { PageId } from './types';
import { PAGE_IDS } from './types';

const AppContent = (): React.JSX.Element => {
    const location = useLocation();
    const navigate = useNavigate();

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

    return (
        <MainMenu
            currentPage={currentPage}
            onPageChange={handlePageChange}
            renderContent={() => (
                <Routes>
                    <Route path="/" element={<Navigate to="/inspect" replace />} />
                    <Route path="/neighbours" element={<NeighboursPage />} />
                    <Route path="/inspect" element={<InspectPage />} />
                    <Route path="/route" element={<RoutePage />} />
                    <Route path="/functions" element={<FunctionsPage />} />
                    <Route path="/pipelines" element={<PipelinesPage />} />
                </Routes>
            )}
        />
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
