// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {api} from './api';

type PostLike = {props?: Record<string, unknown>} | undefined;

const eventIdPrefix = 'matrix_event_id_';

// matrixEventId returns the Matrix event a post was synced to, if any. The
// bridge stores it in a post property named after the homeserver.
export function matrixEventId(post: PostLike): string {
    const props = post?.props;
    if (!props) {
        return '';
    }
    for (const key of Object.keys(props)) {
        const value = props[key];
        if (key.startsWith(eventIdPrefix) && typeof value === 'string' && value !== '') {
            return value;
        }
    }
    return '';
}

// copyMatrixEventId puts the post's Matrix event ID on the clipboard.
export async function copyMatrixEventId(post: PostLike): Promise<boolean> {
    const id = matrixEventId(post);
    if (!id) {
        return false;
    }
    await navigator.clipboard.writeText(id);
    return true;
}

// openInMatrix opens the post's event in a new tab and returns the link, or
// an empty string when the post is not in Matrix.
export async function openInMatrix(postId: string, open: (url: string) => void = (url) => window.open(url, '_blank', 'noopener')): Promise<string> {
    const info = await api.postMatrix(postId);
    if (!info.bridged || !info.permalink) {
        return '';
    }
    open(info.permalink);
    return info.permalink;
}
