import React, { useState } from 'react';
import { AsideHeader } from '@gravity-ui/navigation';
import type { MenuItem as AsideHeaderMenuItem } from '@gravity-ui/navigation';
import { Link, Eye, Route, CurlyBracketsFunction, ListUl, HardDrive, LayoutCellsLarge, CirclePlay, Shield, ArrowRight } from '@gravity-ui/icons';
import Logo from './icons/Logo';
import type { PageId } from './types';

interface MainMenuProps {
    currentPage: PageId;
    onPageChange: (pageId: PageId) => void;
    renderContent: () => React.JSX.Element;
    disabled?: boolean;
}

type MenuItem = AsideHeaderMenuItem & {
    id: PageId;
    current: boolean;
}

const MainMenu = ({ currentPage, onPageChange, renderContent, disabled = false }: MainMenuProps): React.JSX.Element => {
    const [compact, setCompact] = useState<boolean>(false);

    const createMenuItem = (id: PageId, title: string, icon: MenuItem['icon']): MenuItem => ({
        id,
        title,
        icon,
        current: currentPage === id,
        onItemClick: disabled ? undefined : () => {
            onPageChange(id);
        },
        className: disabled ? 'main-menu__item--disabled' : undefined,
    });

    const menuItems: MenuItem[] = [
        createMenuItem('inspect', 'Inspect', Eye),
        createMenuItem('functions', 'Functions', CurlyBracketsFunction),
        createMenuItem('pipelines', 'Pipelines', ListUl),
        createMenuItem('devices', 'Devices', HardDrive),
        createMenuItem('neighbours', 'Neighbours', Link),
        createMenuItem('route', 'Route', Route),
        createMenuItem('forward', 'Forward', ArrowRight),
        createMenuItem('decap', 'Decap', LayoutCellsLarge),
        createMenuItem('acl', 'ACL', Shield),
        createMenuItem('pdump', 'Pdump', CirclePlay),
    ];

    return (
        <AsideHeader
            headerDecoration
            compact={compact}
            onChangeCompact={disabled ? undefined : setCompact}
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
