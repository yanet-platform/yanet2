import React, { useState } from 'react';
import { Box, Text, Icon, Button } from '@gravity-ui/uikit';
import { ChevronDown, ChevronUp } from '@gravity-ui/icons';
import type { IconData } from '@gravity-ui/uikit';
import './inspect.scss';

export type InspectSectionVariant = 'modules' | 'devices' | 'pipelines' | 'functions' | 'configs' | 'agents';

export interface InspectSectionProps {
    title: string;
    icon?: IconData;
    count?: number;
    variant?: InspectSectionVariant;
    collapsible?: boolean;
    defaultExpanded?: boolean;
    children: React.ReactNode;
}

export const InspectSection: React.FC<InspectSectionProps> = ({
    title,
    icon,
    count,
    variant,
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

    const sectionClass = variant 
        ? `inspect-section inspect-section--${variant}` 
        : 'inspect-section';

    return (
        <Box className={sectionClass}>
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
