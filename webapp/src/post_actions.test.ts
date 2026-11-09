// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {api} from './api';
import {copyMatrixEventId, matrixEventId, openInMatrix} from './post_actions';

// The bundler resolves the @ alias; jest has no such mapping.
jest.mock('@/manifest', () => ({__esModule: true, default: {id: 'com.mattermost.plugin-matrix-bridge'}}), {virtual: true});

describe('matrixEventId', () => {
    it('reads the event ID the bridge stored on the post', () => {
        expect(matrixEventId({props: {matrix_event_id_example_com: '$abc'}})).toBe('$abc');
    });

    it('is empty for posts that were not synced', () => {
        expect(matrixEventId({props: {other: 'x'}})).toBe('');
        expect(matrixEventId({props: {matrix_event_id_example_com: ''}})).toBe('');
        expect(matrixEventId({})).toBe('');
        expect(matrixEventId(undefined)).toBe('');
    });
});

describe('copyMatrixEventId', () => {
    it('copies the ID to the clipboard', async () => {
        const writeText = jest.fn().mockResolvedValue(undefined);
        Object.assign(navigator, {clipboard: {writeText}});

        expect(await copyMatrixEventId({props: {matrix_event_id_h: '$e1'}})).toBe(true);
        expect(writeText).toHaveBeenCalledWith('$e1');
    });

    it('does nothing without an event', async () => {
        const writeText = jest.fn();
        Object.assign(navigator, {clipboard: {writeText}});

        expect(await copyMatrixEventId({props: {}})).toBe(false);
        expect(writeText).not.toHaveBeenCalled();
    });
});

describe('openInMatrix', () => {
    afterEach(() => jest.restoreAllMocks());

    it('opens the permalink the server gives', async () => {
        jest.spyOn(api, 'postMatrix').mockResolvedValue({bridged: true, permalink: 'https://matrix.to/#/!r:h/$e'});
        const open = jest.fn();

        expect(await openInMatrix('post1', open)).toBe('https://matrix.to/#/!r:h/$e');
        expect(open).toHaveBeenCalledWith('https://matrix.to/#/!r:h/$e');
    });

    it('opens nothing for a post that is not in Matrix', async () => {
        jest.spyOn(api, 'postMatrix').mockResolvedValue({bridged: false});
        const open = jest.fn();

        expect(await openInMatrix('post1', open)).toBe('');
        expect(open).not.toHaveBeenCalled();
    });
});
