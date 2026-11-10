// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';

import BridgeStatusView from './view';

import {api} from '@/api';
import type {AdminStatus} from '@/api';

// BridgeStatus is the System Console page: health, what is bridged and what
// is waiting, in place of reading /matrix doctor, list and status output.
const BridgeStatus = () => {
    const [status, setStatus] = useState<AdminStatus | null>(null);
    const [error, setError] = useState('');

    const load = useCallback(async () => {
        setError('');
        try {
            setStatus(await api.adminStatus());
        } catch (err) {
            setError(err instanceof Error ? err.message : String(err));
        }
    }, []);

    useEffect(() => {
        load();
    }, [load]);

    return (
        <BridgeStatusView
            status={status}
            error={error}
            onRefresh={load}
        />
    );
};

export default BridgeStatus;
