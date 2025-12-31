import React, { useState } from 'react';
import { Box, Text, Icon, Button } from '@gravity-ui/uikit';
import { ChevronDown, ChevronUp } from '@gravity-ui/icons';
import type { IconData } from '@gravity-ui/uikit';
import './inspect.scss';

export interface InspectSectionProps {
    title: string;
    icon?: IconData;
    count?: number;
    collapsible?: boolean;
    defaultExpanded?: boolean;
    children: React.ReactNode;
}

export const InspectSection: React.FC<InspectSectionProps> = ({
    title,
    icon,
    count,
    collapsible = false,
    defaultExpanded = true,
    children,
}) => {
    const [expanded, setExpanded] = useState(defaultExpanded);

    const handleToggle = () => {
        if (collapsible) {
            setExpanded((prev) => !prev);
        }
    };

    return (
        <Box className="inspect-section">
            <Box
                className={`inspect-section__header ${collapsible ? 'inspect-section__header--clickable' : ''}`}
                onClick={collapsible ? handleToggle : undefined}
            >
                <Box className="inspect-section__title-group">
                    {icon && (
                        <Box className="inspect-section__icon">
                            <Icon data={icon} size={18} />
                        </Box>
                    )}
                    <Text variant="subheader-2" className="inspect-section__title">
                        {title}
                    </Text>
                    {count !== undefined && (
                        <Text variant="body-1" color="secondary" className="inspect-section__count">
                            ({count})
                        </Text>
                    )}
                </Box>
                {collapsible && (
                    <Button view="flat" size="s" className="inspect-section__toggle">
                        <Icon data={expanded ? ChevronUp : ChevronDown} size={16} />
                    </Button>
                )}
            </Box>
            {expanded && (
                <Box className="inspect-section__content">
                    {children}
                </Box>
            )}
        </Box>
    );
};
