// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import ReactDOM from 'react-dom';
import {act} from 'react-dom/test-utils';

import MatrixCallCard, {formatDuration, safeJoinURL} from './index';

function render(props: Record<string, unknown>) {
    const container = document.createElement('div');
    document.body.appendChild(container);
    act(() => {
        ReactDOM.render(<MatrixCallCard post={{props}}/>, container);
    });
    return container;
}

describe('MatrixCallCard', () => {
    test('shows a running call with participants and a join link', () => {
        const c = render({start_at: 1000, end_at: 0, participants: ['Alice', 'Bob'], join_url: 'https://element.example/#/room/!r:hs'});
        expect(c.textContent).toContain('Call in progress');
        expect(c.textContent).toContain('Alice, Bob');
        const link = c.querySelector('a');
        expect(link?.getAttribute('href')).toBe('https://element.example/#/room/!r:hs');
        expect(link?.getAttribute('rel')).toContain('noopener');
    });

    test('shows an ended call with its duration and no join link', () => {
        const c = render({start_at: 1000, end_at: 1000 + (12 * 60000), participants: ['Alice'], join_url: 'https://x.example'});
        expect(c.textContent).toBe('📞 Call in MatrixCall ended · 12 min · Alice');
    });

    test('formats durations', () => {
        expect(formatDuration(1000, 1000 + (12 * 60000))).toBe('12 min');
        expect(formatDuration(1000, 1000 + (65 * 60000))).toBe('1 h 5 min');
        expect(formatDuration(1000, 1000 + (60 * 60000))).toBe('1 h');
        expect(formatDuration(0, 0)).toBe('');
    });

    test('refuses links that are not http(s)', () => {
        /* eslint-disable no-script-url */
        expect(safeJoinURL('javascript:alert(1)')).toBe('');
        expect(safeJoinURL('data:text/html,x')).toBe('');
        expect(safeJoinURL('https://ok.example')).toBe('https://ok.example');
        expect(safeJoinURL(42)).toBe('');
        const c = render({join_url: 'javascript:alert(1)'});
        expect(c.querySelector('a')).toBeNull();
        /* eslint-enable no-script-url */
    });
});
