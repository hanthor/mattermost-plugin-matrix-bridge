// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Store, Action} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import HomeserverConfig from '@/components/admin_console_settings/homeserver_config';
import RegistrationDownload from '@/components/admin_console_settings/registration_download';
import MatrixCallCard from '@/components/matrix_call_card';
import manifest from '@/manifest';
import type {PluginRegistry} from '@/types/mattermost-webapp';

export default class Plugin {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars, @typescript-eslint/no-empty-function
    public async initialize(registry: PluginRegistry, store: Store<GlobalState, Action<Record<string, unknown>>>) {
        // Register custom admin console components
        registry.registerAdminConsoleCustomSetting('registration_download', RegistrationDownload, {showTitle: false});
        registry.registerAdminConsoleCustomSetting('homeserver_config', HomeserverConfig, {showTitle: false});

        // Calls started in Matrix rooms are shown as a card with a join button
        registry.registerPostTypeComponent('custom_matrix_call', MatrixCallCard);
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void;
    }
}

window.registerPlugin(manifest.id, new Plugin());
