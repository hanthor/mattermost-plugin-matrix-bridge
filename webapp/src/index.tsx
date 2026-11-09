// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import type {Store, Action} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import HomeserverConfig from '@/components/admin_console_settings/homeserver_config';
import RegistrationDownload from '@/components/admin_console_settings/registration_download';
import BridgePanel from '@/components/bridge_panel';
import MatrixCallCard from '@/components/matrix_call_card';
import manifest from '@/manifest';
import {copyMatrixEventId, matrixEventId, openInMatrix} from '@/post_actions';
import type {PluginRegistry} from '@/types/mattermost-webapp';

const MatrixIcon = () => (
    <svg
        width='18'
        height='18'
        viewBox='0 0 24 24'
        fill='none'
        stroke='currentColor'
        strokeWidth='2'
        aria-hidden='true'
    >
        <path d='M7 3H4v18h3M17 3h3v18h-3'/>
        <path d='M10 8v8M14 8v8'/>
    </svg>
);

export default class Plugin {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars, @typescript-eslint/no-empty-function
    public async initialize(registry: PluginRegistry, store: Store<GlobalState, Action<Record<string, unknown>>>) {
        // Register custom admin console components
        registry.registerAdminConsoleCustomSetting('registration_download', RegistrationDownload, {showTitle: false});
        registry.registerAdminConsoleCustomSetting('homeserver_config', HomeserverConfig, {showTitle: false});

        // Calls started in Matrix rooms are shown as a card with a join button
        registry.registerPostTypeComponent('custom_matrix_call', MatrixCallCard);

        // Post menu: jump to the message in Matrix, or copy its event ID
        const postOf = (postId: string) => (store.getState() as unknown as {entities: {posts: {posts: Record<string, {props?: Record<string, unknown>}>}}}).entities.posts.posts[postId];
        const isInMatrix = (postId: string) => matrixEventId(postOf(postId)) !== '';
        registry.registerPostDropdownMenuAction('Open in Matrix', (postId: string) => {
            openInMatrix(postId).catch(() => undefined);
        }, isInMatrix);
        registry.registerPostDropdownMenuAction('Copy Matrix event ID', (postId: string) => {
            copyMatrixEventId(postOf(postId)).catch(() => undefined);
        }, isInMatrix);

        // A panel with the channel's bridge status and the person's Matrix account
        const {toggleRHSPlugin} = registry.registerRightHandSidebarComponent(BridgePanel, 'Matrix bridge');
        registry.registerChannelHeaderButtonAction(
            <MatrixIcon/>,
            () => store.dispatch(toggleRHSPlugin as never),
            'Matrix bridge',
            'Matrix bridge',
        );
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void;
    }
}

window.registerPlugin(manifest.id, new Plugin());
