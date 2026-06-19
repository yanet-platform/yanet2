import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AsideHeader, FooterItem } from '@gravity-ui/navigation';
import type { MenuItem as AsideHeaderMenuItem } from '@gravity-ui/navigation';
import { Server } from '@gravity-ui/icons';
import Logo from './icons/Logo';
import type { PageId } from './types';
import { navItems } from './navItems';
import type { NavSection } from './navItems';
import { useGateways } from './gateways';
import './MainMenu.scss';

interface MainMenuProps {
    currentPage: PageId;
    onPageChange: (pageId: PageId) => void;
    renderContent: () => React.JSX.Element;
    disabled?: boolean;
    onOpenGatewayDrawer?: () => void;
    onToggleGatewayDrawer?: () => void;
    gatewayDrawerOpen?: boolean;
    onAsideSize?: (size: number) => void;
}

type NavMenuItem = AsideHeaderMenuItem & {
    id: string;
    current: boolean;
};

const MainMenu = ({
    currentPage,
    onPageChange,
    renderContent,
    disabled = false,
    onOpenGatewayDrawer,
    onToggleGatewayDrawer,
    gatewayDrawerOpen = false,
    onAsideSize,
}: MainMenuProps): React.JSX.Element => {
    const [compact, setCompact] = useState<boolean>(false);
    const navigate = useNavigate();
    const { activeGateway } = useGateways();

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

    const menuItems: NavMenuItem[] = [];
    let lastSection: NavSection | null = null;
    let dividerIdx = 0;

    for (const item of navItems) {
        if (item.section !== lastSection) {
            if (lastSection !== null) {
                menuItems.push(createDivider(`__div_${dividerIdx++}`));
            }
            menuItems.push(createSectionHeader(`__section_${item.section}`, item.section));
            lastSection = item.section;
        }
        menuItems.push(createMenuItem(item.id, item.title, item.icon));
    }

    const gatewayTitle = activeGateway ? `Gateway: ${activeGateway.host}` : 'Gateways';

    return (
        <AsideHeader
            headerDecoration
            compact={compact}
            onChangeCompact={disabled ? undefined : setCompact}
            menuItems={menuItems}
            logo={{
                icon: () => <Logo size={24} />,
                text: 'YANET',
                href: '/builtin/dashboard',
                onClick: (event) => {
                    event.preventDefault();
                    if (!disabled) {
                        navigate('/builtin/dashboard');
                    }
                },
            }}
            renderFooter={(onOpenGatewayDrawer || onToggleGatewayDrawer)
                ? ({ size, compact: isCompact }) => {
                    if (onAsideSize) {
                        onAsideSize(size);
                    }
                    const handleGatewayClick = onToggleGatewayDrawer ?? onOpenGatewayDrawer;
                    return (
                        <FooterItem
                            compact={isCompact}
                            item={{
                                id: '__gateways',
                                title: gatewayTitle,
                                tooltipText: gatewayTitle,
                                icon: Server,
                                current: gatewayDrawerOpen,
                                onItemClick: disabled ? undefined : handleGatewayClick,
                            }}
                        />
                    );
                }
                : undefined}
            renderContent={renderContent}
        />
    );
};

export default MainMenu;
