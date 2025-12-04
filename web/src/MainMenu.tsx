import React, { useState } from 'react';
import { AsideHeader } from '@gravity-ui/navigation';
import type { MenuItem as AsideHeaderMenuItem } from '@gravity-ui/navigation';
import { Link, Eye, Route, Server, LayoutList } from '@gravity-ui/icons';
import Logo from './icons/Logo';
import type { PageId } from './types';

interface MainMenuProps {
    currentPage: PageId;
    onPageChange: (pageId: PageId) => void;
    renderContent: () => React.JSX.Element;
}

type MenuItem = AsideHeaderMenuItem & {
    id: PageId;
    current: boolean;
}

const MainMenu = ({ currentPage, onPageChange, renderContent }: MainMenuProps): React.JSX.Element => {
    const [compact, setCompact] = useState<boolean>(false);

    const menuItems: MenuItem[] = [
        {
            id: 'inspect',
            title: 'Inspect',
            icon: Eye,
            current: currentPage === 'inspect',
            onItemClick: () => {
                onPageChange('inspect');
            },
        },
        {
            id: 'functions',
            title: 'Network Functions',
            icon: Server,
            current: currentPage === 'functions',
            onItemClick: () => {
                onPageChange('functions');
            },
        },
        {
            id: 'pipelines',
            title: 'Pipelines',
            icon: LayoutList,
            current: currentPage === 'pipelines',
            onItemClick: () => {
                onPageChange('pipelines');
            },
        },
        {
            id: 'neighbours',
            title: 'Neighbours',
            icon: Link,
            current: currentPage === 'neighbours',
            onItemClick: () => {
                onPageChange('neighbours');
            },
        },
        {
            id: 'route',
            title: 'Route',
            icon: Route,
            current: currentPage === 'route',
            onItemClick: () => {
                onPageChange('route');
            },
        },
    ];

    return (
        <AsideHeader
            headerDecoration
            compact={compact}
            onChangeCompact={setCompact}
            menuItems={menuItems}
            logo={{
                icon: () => <Logo size={24} />,
                text: 'YANET',
            }}
            renderContent={renderContent}
        />
    );
};

export default MainMenu;
