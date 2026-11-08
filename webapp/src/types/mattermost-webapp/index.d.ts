// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export interface PluginRegistry {
    registerPostTypeComponent(typeName: string, component: React.ElementType);
    registerAdminConsoleCustomSetting(key: string, component: React.ElementType, options?: {showTitle?: boolean});

    registerChannelHeaderButtonAction(icon: React.ReactElement, action: () => void, dropdownText: string, tooltipText?: string): string;
    registerRightHandSidebarComponent(component: React.ElementType, title: string | React.ReactElement): {id: string; showRHSPlugin: unknown; hideRHSPlugin: unknown; toggleRHSPlugin: unknown};

    // Add more if needed from https://developers.mattermost.com/extend/plugins/webapp/reference
}
