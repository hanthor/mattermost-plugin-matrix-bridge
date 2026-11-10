// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import ReactDOM from 'react-dom';
import {act} from 'react-dom/test-utils';

import BridgePanelView from './view';
import type {Props} from './view';

import type {ChannelStatus, Me} from '@/api';

const me = (over: Partial<Me> = {}): Me => ({
    enabled: true,
    mode: 'ghost',
    matrix_user_id: '@_mattermost_abc:example.com',
    linked: false,
    puppeted: false,
    claimed: false,
    can_claim: false,
    claim_gives_password: false,
    bot_user: '@_mattermost_bridge:example.com',
    cutover: false,
    is_admin: false,
    ...over,
});

const channel = (over: Partial<ChannelStatus> = {}): ChannelStatus => ({
    mapped: true,
    room_id: '!r:example.com',
    room_name: 'Bridge Demo',
    element_url: 'https://element.example.com/#/room/!r:example.com',
    direct: false,
    can_manage: true,
    can_create: false,
    calls_mode: 'presence',
    can_call: false,
    cutover: false,
    mode: 'ghost',
    participants: 5,
    ...over,
});

const props = (over: Partial<Props> = {}): Props => ({
    me: me(),
    channel: channel(),
    error: '',
    claim: null,
    linkCode: null,
    callURL: '',
    busy: false,
    onClaim: jest.fn(),
    onStartLink: jest.fn(),
    onUnlink: jest.fn(),
    onStartCall: jest.fn(),
    onBridge: jest.fn(),
    ...over,
});

let container: HTMLDivElement;

beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
});

afterEach(() => {
    ReactDOM.unmountComponentAtNode(container);
    container.remove();
});

function render(p: Props) {
    act(() => {
        ReactDOM.render(<BridgePanelView {...p}/>, container);
    });
}

const text = () => container.textContent || '';

function find(selector: string, content: string) {
    return Array.from(container.querySelectorAll(selector)).find((el) => (el.textContent || '').includes(content)) as HTMLElement | undefined;
}

function click(content: string) {
    const button = find('button', content);
    expect(button).toBeTruthy();
    act(() => {
        button!.dispatchEvent(new MouseEvent('click', {bubbles: true}));
    });
}

describe('BridgePanelView', () => {
    test('shows a bridged channel with its room and a link to open it', () => {
        render(props());
        expect(text()).toContain('✅ Bridged to Matrix');
        expect(text()).toContain('Bridge Demo · 5 people in the room');
        expect(find('a', 'Open the room')?.getAttribute('href')).toBe('https://element.example.com/#/room/!r:example.com');
        expect(find('button', 'Start a call')).toBeUndefined();
    });

    test('warns when the room is encrypted and the bridge cannot handle it', () => {
        render(props({channel: channel({encrypted: true, encryption_works: false})}));
        expect(text()).toContain('cannot read: messages are not synced in either direction');
        render(props({channel: channel({encrypted: true, encryption_works: true})}));
        expect(text()).toContain('the bridge encrypts and decrypts its messages');
        render(props({channel: channel({encrypted: false})}));
        expect(container.querySelector('[data-testid="bridge-encrypted"]')).toBeNull();
    });

    test('offers a call when the room can have one, and shows its link after', () => {
        const onStartCall = jest.fn();
        render(props({channel: channel({can_call: true}), onStartCall}));
        click('Start a call');
        expect(onStartCall).toHaveBeenCalled();
        render(props({channel: channel({can_call: true}), callURL: 'https://call.example.com/x'}));
        expect(find('a', 'join')?.getAttribute('href')).toBe('https://call.example.com/x');
    });

    test('lets someone who may bridge an unbridged channel do it with a button', () => {
        const onBridge = jest.fn();
        render(props({channel: channel({mapped: false, can_create: true}), onBridge}));
        click('Bridge this channel');
        expect(onBridge).toHaveBeenCalled();
        expect(text()).toContain('/matrix map');
    });

    test('tells everyone else who can bridge an unbridged channel', () => {
        render(props({channel: channel({mapped: false, can_create: true})}));
        render(props({channel: channel({mapped: false, can_create: false})}));
        expect(text()).toContain('A channel admin can bridge this channel.');
        render(props({channel: channel({mapped: false, cutover: true})}));
        expect(text()).toContain('Cut-over mode is on: no new rooms are bridged.');
    });

    test('lets a mirror-mode user claim their account and shows the one-time password', () => {
        const onClaim = jest.fn();
        render(props({me: me({mode: 'mirror', matrix_user_id: '@ada:example.com', can_claim: true}), onClaim}));
        expect(text()).toContain('Your Matrix account was created for you. Claim it to sign in yourself.');
        expect(find('button', 'Link an existing Matrix account')).toBeUndefined();
        click('Claim my Matrix account');
        expect(onClaim).toHaveBeenCalled();

        render(props({me: me({mode: 'mirror', claimed: true}), claim: {matrix_user_id: '@ada:example.com', one_time_password: 'S3cretPassw0rd'}}));
        expect(text()).toContain('S3cretPassw0rd');
        expect(text().split('is now yours').length - 1).toBe(1);
        expect(text()).not.toContain('Your Matrix account is yours');
        expect(find('button', 'Claim my Matrix account')).toBeUndefined();
    });

    test('walks a ghost-mode user through linking and unlinking', () => {
        const onStartLink = jest.fn();
        const onUnlink = jest.fn();
        render(props({onStartLink}));
        click('Link an existing Matrix account');
        expect(onStartLink).toHaveBeenCalled();

        render(props({linkCode: {code: 'ABCD2345', bot_user: '@_mattermost_bridge:example.com', command: '!mattermost link ABCD2345', expires_min: 15}}));
        expect(text()).toContain('!mattermost link ABCD2345');
        expect(text()).toContain('The code expires in 15 minutes.');

        render(props({me: me({linked: true, matrix_user_id: '@ada:example.com'}), onUnlink}));
        click('Unlink');
        expect(onUnlink).toHaveBeenCalled();
    });

    test('explains when the bridge is not set up and shows request errors', () => {
        render(props({me: me({enabled: false})}));
        expect(text()).toContain('bridge is not set up yet');
        render(props({me: null, channel: null, error: 'not signed in'}));
        expect(container.querySelector('[data-testid="bridge-error"]')?.textContent).toBe('not signed in');
    });

    test('lists who is in the room and where they are from', () => {
        render(props({
            channel: channel({mapped: true, participants: 3}),
            members: [
                {matrix_id: '@ada:matrix.org', homeserver: 'matrix.org', kind: 'matrix'},
                {matrix_id: '@_mattermost_bob:example.com', homeserver: 'example.com', kind: 'mattermost'},
                {matrix_id: '@_mattermost_bridge:example.com', homeserver: 'example.com', kind: 'bot'},
            ],
        }));
        const list = container.querySelector('[data-testid="bridge-members"]')?.textContent || '';
        expect(list).toContain('@ada:matrix.org · on Matrix · matrix.org');
        expect(list).toContain('@_mattermost_bob:example.com · from Mattermost');
        expect(list).toContain('@_mattermost_bridge:example.com · the bridge');
    });
});
