// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import ReactDOM from 'react-dom';
import {act} from 'react-dom/test-utils';

import BridgeStatusView from './view';

import type {AdminStatus} from '@/api';

const status: AdminStatus = {
    enabled: true,
    mode: 'mirror',
    worst: 'warn',
    health: [
        {status: 'ok', title: 'Homeserver'},
        {status: 'warn', title: 'Element Web address', detail: 'not set'},
    ],
    mappings: [{channel_id: 'c1', channel_name: 'Town Square', team: 'Main', room: '!r:example.com'}],
    mapped_channels: 3,
    mirror: {accounts: 10, claimed: 4, collisions: 1},
    dead_letters: 2,
    cutover: false,
};

let container: HTMLDivElement;

beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
});

afterEach(() => {
    ReactDOM.unmountComponentAtNode(container);
    container.remove();
});

function render(element: React.ReactElement) {
    act(() => {
        ReactDOM.render(element, container);
    });
}

const text = () => container.textContent || '';
const byTestId = (id: string) => container.querySelector(`[data-testid="${id}"]`);

describe('BridgeStatusView', () => {
    it('shows health, overview and the bridged channels', () => {
        render(
            <BridgeStatusView
                status={status}
                error=''
                onRefresh={() => undefined}
            />,
        );
        expect(text()).toContain('Homeserver');
        expect(text()).toContain('not set');
        expect(text()).toContain('3 bridged channels');
        expect(text()).toContain('10 Matrix accounts, 4 claimed, 1 renamed to avoid a clash');
        expect(byTestId('bridge-status-dead')?.textContent).toContain('2 changes could not be delivered');
        expect(text()).toContain('Town Square');
        expect(text()).toContain('Showing 1 of 3.');
    });

    it('shows an error instead of a half-empty page', () => {
        render(
            <BridgeStatusView
                status={null}
                error='only system administrators can see this'
                onRefresh={() => undefined}
            />,
        );
        expect(byTestId('bridge-status-error')?.textContent).toContain('only system administrators');
    });

    it('says when nothing is waiting', () => {
        render(
            <BridgeStatusView
                status={{...status, dead_letters: 0, mappings: [], mapped_channels: 0, mode: 'ghost'}}
                error=''
                onRefresh={() => undefined}
            />,
        );
        expect(byTestId('bridge-status-dead')?.textContent).toBe('Nothing is waiting to be retried');
        expect(text()).toContain('No channel is bridged yet.');
    });
});
