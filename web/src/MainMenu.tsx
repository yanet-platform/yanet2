import React, { useState } from 'react';
import { AsideHeader } from '@gravity-ui/navigation';
import type { MenuItem as AsideHeaderMenuItem } from '@gravity-ui/navigation';
import { Link, Eye, Route, CurlyBracketsFunction, ListUl, HardDrive, LayoutCellsLarge, CirclePlay, Shield, ArrowRight } from '@gravity-ui/icons';
import Logo from './icons/Logo';
import type { PageId } from './types';
import './MainMenu.scss';

interface MainMenuProps {
    currentPage: PageId;
    onPageChange: (pageId: PageId) => void;
    renderContent: () => React.JSX.Element;
    disabled?: boolean;
}

type NavMenuItem = AsideHeaderMenuItem & {
    id: string;
    current: boolean;
};

const MainMenu = ({ currentPage, onPageChange, renderContent, disabled = false }: MainMenuProps): React.JSX.Element => {
    const [compact, setCompact] = useState<boolean>(false);

    const createMenuItem = (id: PageId, title: string, icon: NavMenuItem['icon']): NavMenuItem => ({
        id,
        title,
        icon,
        current: currentPage === id,
        onItemClick: disabled ? undefined : () => {
            onPageChange(id);
        },
        className: disabled ? 'main-menu__item--disabled' : undefined,
    });

    const createSectionHeader = (id: string, title: string): NavMenuItem => ({
        id,
        title,
        current: false,
        onItemClick: undefined,
        className: 'main-menu__section-header',
        itemWrapper: (_params, _makeItem, opts) => {
            if (opts?.compact || opts?.collapsed) {
                return null;
            }
            return (
                <span className="main-menu__section-label">{title}</span>
            );
        },
    });

    const createDivider = (id: string): NavMenuItem => ({
        id,
        title: '',
        type: 'divider' as const,
        current: false,
        onItemClick: undefined,
    });

    const menuItems: NavMenuItem[] = [
        createSectionHeader('__section_builtin', 'Builtin'),
        createMenuItem('builtin/inspect', 'Inspect', Eye),
        createMenuItem('builtin/functions', 'Functions', CurlyBracketsFunction),
        createMenuItem('builtin/pipelines', 'Pipelines', ListUl),
        createMenuItem('builtin/devices', 'Devices', HardDrive),
        createDivider('__div_1'),
        createSectionHeader('__section_modules', 'Modules'),
        createMenuItem('modules/forward', 'Forward', ArrowRight),
        createMenuItem('modules/route', 'Route', Route),
        createMenuItem('modules/decap', 'Decap', LayoutCellsLarge),
        createMenuItem('modules/acl', 'ACL', Shield),
        createMenuItem('modules/pdump', 'Pdump', CirclePlay),
        createDivider('__div_2'),
        createSectionHeader('__section_operators', 'Operators'),
        createMenuItem('operators/route', 'Route', Route),
        createMenuItem('operators/neighbours', 'Neighbours', Link),
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
