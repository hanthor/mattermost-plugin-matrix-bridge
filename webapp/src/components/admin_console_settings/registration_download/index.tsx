// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import {useSelector} from 'react-redux';

import {getConfig} from 'mattermost-redux/selectors/entities/general';

interface Props {
    label: string;
    helpText?: React.ReactNode;
    config?: AdminConsoleConfig;
}

interface AdminConsoleConfig {
    matrix_server_url?: string;
    matrix_as_token?: string;
    matrix_hs_token?: string;
    matrix_server_domain?: string;
    enable_mirror_mode?: boolean;
}

const RegistrationDownload: React.FC<Props> = ({label, helpText, config}) => {
    const mattermostConfig = useSelector(getConfig);
    const siteURL = mattermostConfig.SiteURL;
    const [isDownloadEnabled, setIsDownloadEnabled] = useState(false);
    const [currentValues, setCurrentValues] = useState<AdminConsoleConfig>({
        matrix_server_url: '',
        matrix_as_token: '',
        matrix_hs_token: '',
        matrix_server_domain: '',
        enable_mirror_mode: false,
    });

    useEffect(() => {
        const checkValues = () => {
            // Get current values - input for text fields, div for generated fields
            const serverUrlInput = document.querySelector('input[id$="matrix_server_url"]') as HTMLInputElement;
            const asTokenDiv = document.querySelector('div[id$="matrix_as_token"]') as HTMLDivElement;
            const hsTokenDiv = document.querySelector('div[id$="matrix_hs_token"]') as HTMLDivElement;
            const serverDomainInput = document.querySelector('input[id$="matrix_server_domain"]') as HTMLInputElement;
            const mirrorModeInput = document.querySelector('input[id$="enable_mirror_mode"][value="true"]') as HTMLInputElement;

            const values = {
                matrix_server_url: serverUrlInput?.value || config?.matrix_server_url || '',
                matrix_as_token: asTokenDiv?.textContent || config?.matrix_as_token || '',
                matrix_hs_token: hsTokenDiv?.textContent || config?.matrix_hs_token || '',
                matrix_server_domain: serverDomainInput?.value || config?.matrix_server_domain || '',
                enable_mirror_mode: mirrorModeInput ? mirrorModeInput.checked : Boolean(config?.enable_mirror_mode),
            };

            setCurrentValues(values);

            const hasAllValues = Boolean(
                values.matrix_server_url?.trim() &&
                values.matrix_as_token?.trim() &&
                values.matrix_hs_token?.trim() &&
                siteURL?.trim(),
            );
            setIsDownloadEnabled(hasAllValues);
        };

        // Check initially and then periodically
        checkValues();
        const interval = setInterval(checkValues, 500);

        return () => clearInterval(interval);
    }, [config, siteURL]);

    const generateRegistrationFile = () => {
        if (!isDownloadEnabled) {
            return;
        }

        // The server name (domain in user IDs) is the configured Matrix server
        // domain when set, otherwise the hostname of the server URL.
        let domain = currentValues.matrix_server_domain?.trim() || '';
        if (!domain) {
            try {
                if (currentValues.matrix_server_url) {
                    domain = new URL(currentValues.matrix_server_url).hostname;
                }
            } catch (e) {
                domain = 'matrix.org';
            }
        }
        const escapedDomain = domain.replace(/\./g, '\\.');

        // Mirror mode registers real @username:domain accounts, so the bridge
        // must own the whole user namespace of its dedicated homeserver.
        const mirrorNamespace = currentValues.enable_mirror_mode ? `
    - exclusive: true
      regex: "@.*:${escapedDomain}"` : '';

        const registrationYaml = `id: "mattermost-bridge"
url: "${siteURL}/plugins/com.mattermost.plugin-matrix-bridge"
as_token: "${currentValues.matrix_as_token}"
hs_token: "${currentValues.matrix_hs_token}"
sender_localpart: "_mattermost_bridge"
rate_limited: false
namespaces:
  users:
    - exclusive: true
      regex: "@_mattermost_.*:${escapedDomain}"${mirrorNamespace}
  aliases:
    - exclusive: true
      regex: "#_mattermost_.*:${escapedDomain}"
    - exclusive: false
      regex: "#mattermost-bridge-.*:${escapedDomain}"
  rooms: []
# Deliver typing and presence (spec v1.13 name and the MSC2409 name used by older homeservers).
receive_ephemeral: true
de.sorunome.msc2409.push_ephemeral: true
`;

        // Create and download the file
        const blob = new Blob([registrationYaml], {type: 'text/yaml'});
        const url = URL.createObjectURL(blob);
        const link = document.createElement('a');
        link.href = url;
        link.download = 'mattermost-bridge-registration.yaml';
        document.body.appendChild(link);
        link.click();
        document.body.removeChild(link);
        URL.revokeObjectURL(url);
    };

    const buttonStyle = {
        backgroundColor: isDownloadEnabled ? '#1e325c' : '#999',
        color: 'white',
        border: 'none',
        padding: '8px 16px',
        borderRadius: '4px',
        cursor: isDownloadEnabled ? 'pointer' : 'not-allowed',
        fontSize: '14px',
        opacity: isDownloadEnabled ? 1 : 0.6,
    };

    return (
        <div className='form-group'>
            <label className='control-label col-sm-4'>
                {label}
            </label>
            <div className='col-sm-8'>
                <button
                    type='button'
                    style={buttonStyle}
                    onClick={generateRegistrationFile}
                    disabled={!isDownloadEnabled}
                >
                    {'Download Registration File'}
                </button>
                {helpText && (
                    <div className='help-text'>
                        {helpText}
                    </div>
                )}
                {!isDownloadEnabled && (
                    <div
                        className='help-text'
                        style={{color: '#999', marginTop: '8px'}}
                    >
                        {siteURL?.trim() ? 'Please fill in all Matrix configuration fields to enable download.' : 'Please configure the Site URL in System Console > General > Server Settings to enable download.'}
                    </div>
                )}
            </div>
        </div>
    );
};

export default RegistrationDownload;
