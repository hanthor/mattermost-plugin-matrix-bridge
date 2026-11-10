// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import type {AdminStatus} from '@/api';

export type Props = {
    status: AdminStatus | null;
    error: string;
    onRefresh: () => void;
};

const icons = {ok: '✅', warn: '⚠️', fail: '❌'};

const box: React.CSSProperties = {border: '1px solid rgba(var(--center-channel-color-rgb), 0.16)', borderRadius: 4, padding: 16, marginBottom: 16};
const heading: React.CSSProperties = {fontSize: 14, fontWeight: 700, margin: '0 0 8px'};
const muted: React.CSSProperties = {opacity: 0.72};

const BridgeStatusView = ({status, error, onRefresh}: Props) => {
    if (error) {
        return <p data-testid='bridge-status-error'>{error}</p>;
    }
    if (!status) {
        return <p style={muted}>{'Loading the bridge status…'}</p>;
    }
    return (
        <div data-testid='bridge-status'>
            <div style={box}>
                <h4 style={heading}>{`${icons[status.worst]} Health`}</h4>
                {status.enabled ? null : <p>{'Synchronisation is switched off.'}</p>}
                <ul style={{listStyle: 'none', padding: 0, margin: 0}}>
                    {status.health.map((c) => (
                        <li key={c.title}>
                            {`${icons[c.status]} `}<strong>{c.title}</strong>
                            {c.detail ? <span style={muted}>{` — ${c.detail}`}</span> : null}
                        </li>
                    ))}
                </ul>
                <button
                    type='button'
                    className='btn btn-tertiary'
                    onClick={onRefresh}
                    style={{marginTop: 8}}
                >
                    {'Check again'}
                </button>
            </div>

            <div style={box}>
                <h4 style={heading}>{'Overview'}</h4>
                <div>{`Mode: ${status.mode === 'mirror' ? 'mirror (everyone has a Matrix account)' : 'ghosts'}${status.cutover ? ' · cut-over mode on' : ''}`}</div>
                <div>{`${status.mapped_channels} bridged ${status.mapped_channels === 1 ? 'channel' : 'channels'}`}</div>
                {status.mode === 'mirror' && (
                    <div>{`${status.mirror.accounts} Matrix accounts, ${status.mirror.claimed} claimed, ${status.mirror.collisions} renamed to avoid a clash`}</div>
                )}
                <div data-testid='bridge-status-dead'>{status.dead_letters > 0 ? `⚠️ ${status.dead_letters} changes could not be delivered; run /matrix retry` : 'Nothing is waiting to be retried'}</div>
            </div>

            <div style={box}>
                <h4 style={heading}>{'Bridged channels'}</h4>
                {status.mappings.length === 0 ? <p style={muted}>{'No channel is bridged yet.'}</p> : (
                    <table style={{width: '100%', borderCollapse: 'collapse'}}>
                        <thead>
                            <tr style={{textAlign: 'left'}}>
                                <th>{'Channel'}</th>
                                <th>{'Team'}</th>
                                <th>{'Matrix room'}</th>
                            </tr>
                        </thead>
                        <tbody>
                            {status.mappings.map((m) => (
                                <tr key={m.channel_id}>
                                    <td>{m.channel_name}</td>
                                    <td>{m.team || ''}</td>
                                    <td><code>{m.room}</code></td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                )}
                {status.mapped_channels > status.mappings.length && (
                    <p style={muted}>{`Showing ${status.mappings.length} of ${status.mapped_channels}.`}</p>
                )}
            </div>
        </div>
    );
};

export default BridgeStatusView;
