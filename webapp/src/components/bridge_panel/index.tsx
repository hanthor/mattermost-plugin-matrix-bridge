// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import {useSelector} from 'react-redux';

import {getCurrentChannelId} from 'mattermost-redux/selectors/entities/channels';

import BridgePanelView from './view';

import {api} from '@/api';
import type {ChannelStatus, Claim, LinkCode, Me} from '@/api';

const message = (err: unknown) => (err instanceof Error ? err.message : String(err));

// BridgePanel is the right-hand sidebar: what the current channel is bridged
// to and how the signed-in person appears in Matrix.
const BridgePanel = () => {
    const channelId = useSelector(getCurrentChannelId);
    const [me, setMe] = useState<Me | null>(null);
    const [channel, setChannel] = useState<ChannelStatus | null>(null);
    const [error, setError] = useState('');
    const [claim, setClaim] = useState<Claim | null>(null);
    const [linkCode, setLinkCode] = useState<LinkCode | null>(null);
    const [callURL, setCallURL] = useState('');
    const [busy, setBusy] = useState(false);

    const loadMe = useCallback(async () => {
        try {
            setMe(await api.me());
        } catch (err) {
            setError(message(err));
        }
    }, []);

    useEffect(() => {
        loadMe();
    }, [loadMe]);

    useEffect(() => {
        let cancelled = false;
        setChannel(null);
        setCallURL('');
        if (!channelId) {
            return undefined;
        }
        api.channel(channelId).then((status) => {
            if (!cancelled) {
                setChannel(status);
            }
        }).catch((err) => {
            if (!cancelled) {
                setError(message(err));
            }
        });
        return () => {
            cancelled = true;
        };
    }, [channelId]);

    const run = async (fn: () => Promise<void>) => {
        setBusy(true);
        setError('');
        try {
            await fn();
        } catch (err) {
            setError(message(err));
        } finally {
            setBusy(false);
        }
    };

    return (
        <BridgePanelView
            me={me}
            channel={channel}
            error={error}
            claim={claim}
            linkCode={linkCode}
            callURL={callURL}
            busy={busy}
            onClaim={() => run(async () => {
                setClaim(await api.claim());
                await loadMe();
            })}
            onStartLink={() => run(async () => {
                setLinkCode(await api.startLink());
            })}
            onUnlink={() => run(async () => {
                await api.unlink();
                setLinkCode(null);
                await loadMe();
            })}
            onStartCall={() => run(async () => {
                const res = await api.startCall(channelId, '');
                setCallURL(res.join_url);
            })}
        />
    );
};

export default BridgePanel;
