// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import type {ChannelStatus, Claim, LinkCode, Me} from '@/api';

export type Props = {
    me: Me | null;
    channel: ChannelStatus | null;
    error: string;
    claim: Claim | null;
    linkCode: LinkCode | null;
    callURL: string;
    busy: boolean;
    onClaim: () => void;
    onStartLink: () => void;
    onUnlink: () => void;
    onStartCall: () => void;
};

const sectionStyle: React.CSSProperties = {padding: '16px 20px', borderBottom: '1px solid rgba(var(--center-channel-color-rgb), 0.12)'};
const headingStyle: React.CSSProperties = {fontSize: 12, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.4, opacity: 0.64, margin: '0 0 8px'};
const mutedStyle: React.CSSProperties = {opacity: 0.72, margin: '4px 0'};
const buttonStyle: React.CSSProperties = {
    background: 'var(--button-bg)', color: 'var(--button-color)', border: 0, borderRadius: 4, padding: '6px 14px', fontWeight: 600, cursor: 'pointer', marginTop: 8, marginRight: 8,
};
const secondaryButtonStyle: React.CSSProperties = {
    ...buttonStyle, background: 'rgba(var(--button-bg-rgb), 0.08)', color: 'var(--button-bg)',
};
const codeStyle: React.CSSProperties = {fontFamily: 'monospace', background: 'rgba(var(--center-channel-color-rgb), 0.08)', borderRadius: 4, padding: '2px 6px', wordBreak: 'break-all'};

export function ChannelSection({channel, callURL, busy, onStartCall}: Pick<Props, 'channel' | 'callURL' | 'busy' | 'onStartCall'>) {
    if (!channel) {
        return null;
    }
    return (
        <div
            style={sectionStyle}
            data-testid='bridge-channel'
        >
            <h3 style={headingStyle}>{'This channel'}</h3>
            {channel.mapped ? (
                <>
                    <div style={{fontWeight: 600}}>{'✅ Bridged to Matrix'}</div>
                    <p style={mutedStyle}>
                        {channel.room_name ? `${channel.room_name} · ` : ''}
                        {`${channel.participants} ${channel.participants === 1 ? 'person' : 'people'} in the room`}
                    </p>
                    {channel.element_url && (
                        // eslint-disable-next-line @mattermost/use-external-link
                        <a
                            href={channel.element_url}
                            target='_blank'
                            rel='noopener noreferrer'
                        >
                            {'Open the room'}
                        </a>
                    )}
                    {channel.can_call && (
                        <div>
                            <button
                                type='button'
                                style={buttonStyle}
                                disabled={busy}
                                onClick={onStartCall}
                            >
                                {'📞 Start a call'}
                            </button>
                        </div>
                    )}
                    {callURL && (
                        <p style={mutedStyle}>
                            {'Call started: '}
                            {/* eslint-disable-next-line @mattermost/use-external-link */}
                            <a
                                href={callURL}
                                target='_blank'
                                rel='noopener noreferrer'
                            >
                                {'join'}
                            </a>
                        </p>
                    )}
                </>
            ) : (
                <>
                    <div style={{fontWeight: 600}}>{channel.direct ? 'Not bridged' : 'Not bridged to Matrix'}</div>
                    {channel.cutover && <p style={mutedStyle}>{'Cut-over mode is on: no new rooms are bridged.'}</p>}
                    {channel.can_create && (
                        <p style={mutedStyle}>
                            {'Run '}<code>{'/matrix create'}</code>{' to bridge this channel with a new Matrix room, or '}<code>{'/matrix map'}</code>{' to use an existing room.'}
                        </p>
                    )}
                    {!channel.can_create && !channel.cutover && !channel.direct && (
                        <p style={mutedStyle}>{'A channel admin can bridge this channel.'}</p>
                    )}
                </>
            )}
        </div>
    );
}

export function AccountSection({me, claim, linkCode, busy, onClaim, onStartLink, onUnlink}: Pick<Props, 'me' | 'claim' | 'linkCode' | 'busy' | 'onClaim' | 'onStartLink' | 'onUnlink'>) {
    if (!me) {
        return null;
    }
    let state = 'You appear in Matrix as a bridge-managed user.';
    if (me.mode === 'mirror') {
        state = me.claimed ? 'Your Matrix account is yours: sign in to any Matrix client with it.' : 'Your Matrix account was created for you. Claim it to sign in yourself.';
    } else if (me.linked) {
        state = me.puppeted ? 'Your Matrix account is linked and the bridge sends your messages as it.' : 'Your Matrix account is linked.';
    }
    return (
        <div
            style={sectionStyle}
            data-testid='bridge-account'
        >
            <h3 style={headingStyle}>{'Your Matrix account'}</h3>
            {me.matrix_user_id && <div style={{fontWeight: 600}}><span style={codeStyle}>{me.matrix_user_id}</span></div>}
            <p style={mutedStyle}>{state}</p>

            {me.can_claim && !claim && (
                <button
                    type='button'
                    style={buttonStyle}
                    disabled={busy}
                    onClick={onClaim}
                >
                    {'Claim my Matrix account'}
                </button>
            )}
            {claim && (
                <div data-testid='bridge-claimed'>
                    <p style={mutedStyle}>{'✅ '}<span style={codeStyle}>{claim.matrix_user_id}</span>{' is now yours.'}</p>
                    {claim.one_time_password ? (
                        <p style={mutedStyle}>
                            {'One-time password (shown once, change it after you sign in): '}<span style={codeStyle}>{claim.one_time_password}</span>
                        </p>
                    ) : (
                        <p style={mutedStyle}>{'Sign in with your organization’s shared login.'}</p>
                    )}
                </div>
            )}

            {me.mode !== 'mirror' && !me.linked && !linkCode && (
                <button
                    type='button'
                    style={secondaryButtonStyle}
                    disabled={busy}
                    onClick={onStartLink}
                >
                    {'Link an existing Matrix account'}
                </button>
            )}
            {linkCode && (
                <div data-testid='bridge-link-code'>
                    <p style={mutedStyle}>
                        {'In Matrix, send this to '}<span style={codeStyle}>{linkCode.bot_user}</span>{' in a direct message:'}
                    </p>
                    <p style={mutedStyle}><span style={codeStyle}>{linkCode.command}</span></p>
                    <p style={mutedStyle}>{`The code expires in ${linkCode.expires_min} minutes.`}</p>
                </div>
            )}
            {me.mode !== 'mirror' && me.linked && (
                <button
                    type='button'
                    style={secondaryButtonStyle}
                    disabled={busy}
                    onClick={onUnlink}
                >
                    {'Unlink'}
                </button>
            )}
            {me.cutover && <p style={mutedStyle}>{'Cut-over mode is on: accounts are frozen.'}</p>}
        </div>
    );
}

const BridgePanelView = (props: Props) => {
    if (props.error && !props.me && !props.channel) {
        return (
            <div style={sectionStyle}>
                <p data-testid='bridge-error'>{props.error}</p>
            </div>
        );
    }
    if (props.me && !props.me.enabled) {
        return (
            <div style={sectionStyle}>
                <p>{'The Matrix bridge is not set up yet. A system admin can configure it under System Console → Plugins.'}</p>
            </div>
        );
    }
    return (
        <div data-testid='bridge-panel'>
            <ChannelSection {...props}/>
            <AccountSection {...props}/>
            {props.error && (
                <div style={sectionStyle}>
                    <p
                        style={{color: 'var(--error-text)'}}
                        data-testid='bridge-error'
                    >
                        {props.error}
                    </p>
                </div>
            )}
        </div>
    );
};

export default BridgePanelView;
