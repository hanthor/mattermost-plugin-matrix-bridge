// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';

interface Props {
    label: string;
    helpText?: React.ReactNode;
}

const HomeserverConfig: React.FC<Props> = ({label, helpText}) => {
    const [matrixDomain, setMatrixDomain] = useState<string>('MYSERVER_DOMAIN');

    useEffect(() => {
        // Use matrix_server_domain instead of extracting from matrix_server_url
        // matrix_server_domain is the server_name used in Matrix IDs (@user:domain)
        // matrix_server_url is just the API endpoint
        const serverDomainInput = document.querySelector('input[id$="matrix_server_domain"]') as HTMLInputElement;

        const updateMatrixDomain = (domain: string) => {
            if (domain && domain.trim()) {
                setMatrixDomain(domain.trim());
            } else {
                setMatrixDomain('MYSERVER_DOMAIN');
            }
        };

        if (serverDomainInput) {
            // Set initial value
            updateMatrixDomain(serverDomainInput.value?.trim() || '');

            const handleInputChange = (event: Event) => {
                const target = event.target as HTMLInputElement;
                updateMatrixDomain(target.value?.trim() || '');
            };

            // Listen for both input and change events to catch all updates
            serverDomainInput.addEventListener('input', handleInputChange);
            serverDomainInput.addEventListener('change', handleInputChange);

            return () => {
                serverDomainInput.removeEventListener('input', handleInputChange);
                serverDomainInput.removeEventListener('change', handleInputChange);
            };
        }

        return undefined;
    }, []);

    const yamlConfig = `room_list_publication_rules:
  - user_id: "@_mattermost_bridge:${matrixDomain}"
    action: allow
  - user_id: "*"
    action: deny`;

    const copyToClipboard = () => {
        navigator.clipboard.writeText(yamlConfig).then(() => {
            // Could add a toast notification here if needed
        }).catch(() => {
            // Fallback for older browsers
            const textArea = document.createElement('textarea');
            textArea.value = yamlConfig;
            document.body.appendChild(textArea);
            textArea.select();
            document.execCommand('copy');
            document.body.removeChild(textArea);
        });
    };

    const codeBlockStyle = {
        backgroundColor: '#f4f4f4',
        border: '1px solid #ddd',
        borderRadius: '4px',
        padding: '12px',
        fontFamily: 'Monaco, Menlo, "Ubuntu Mono", monospace',
        fontSize: '12px',
        lineHeight: '1.4',
        whiteSpace: 'pre' as const,
        overflow: 'auto',
        margin: '8px 0',
        position: 'relative' as const,
    };

    const copyButtonStyle = {
        position: 'absolute' as const,
        top: '8px',
        right: '8px',
        backgroundColor: '#1e325c',
        color: 'white',
        border: 'none',
        padding: '4px 8px',
        borderRadius: '3px',
        cursor: 'pointer',
        fontSize: '11px',
        opacity: 0.8,
    };

    const instructionStyle = {
        backgroundColor: '#fff3cd',
        border: '1px solid #ffeaa7',
        borderRadius: '4px',
        padding: '12px',
        margin: '8px 0',
        fontSize: '14px',
    };

    return (
        <div className='form-group'>
            <label className='control-label col-sm-4'>
                {label}
            </label>
            <div className='col-sm-8'>
                <div style={instructionStyle}>
                    <strong>{'Required Configuration for Matrix Homeserver'}</strong>
                    <p style={{margin: '8px 0', fontSize: '13px'}}>
                        {'Add this section to your '}
                        <code>{'homeserver.yaml'}</code>
                        {' file to enable room creation by the bridge:'}
                    </p>
                </div>

                <div style={codeBlockStyle}>
                    <button
                        type='button'
                        style={copyButtonStyle}
                        onClick={copyToClipboard}
                        title='Copy to clipboard'
                    >
                        {'Copy'}
                    </button>
                    {yamlConfig}
                </div>

                <div style={{fontSize: '13px', color: '#666', marginTop: '8px'}}>
                    {matrixDomain === 'MYSERVER_DOMAIN' ? (
                        <span style={{color: '#d04444'}}>
                            {'⚠️ Set the Matrix Server Domain field above to see the correct configuration'}
                        </span>
                    ) : (
                        <span style={{color: '#28a745'}}>
                            {'✓ Using server_name: '}
                            <strong>{matrixDomain}</strong>
                        </span>
                    )}
                </div>

                <div style={{fontSize: '13px', color: '#666', marginTop: '12px'}}>
                    <strong>{'Steps:'}</strong>
                    <ol style={{margin: '4px 0', paddingLeft: '20px'}}>
                        <li>{'Copy the configuration above'}</li>
                        <li>
                            {'Add it to your Matrix homeserver&apos;s '}
                            <code>{'homeserver.yaml'}</code>
                            {' file'}
                        </li>
                        <li>{'Restart your Matrix homeserver'}</li>
                    </ol>
                </div>

                {helpText && (
                    <div
                        className='help-text'
                        style={{marginTop: '12px'}}
                    >
                        {helpText}
                    </div>
                )}
            </div>
        </div>
    );
};

export default HomeserverConfig;
