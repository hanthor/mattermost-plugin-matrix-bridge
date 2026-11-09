// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from '@/manifest';

export type Me = {
    enabled: boolean;
    mode: 'ghost' | 'mirror';
    matrix_user_id: string;
    linked: boolean;
    puppeted: boolean;
    claimed: boolean;
    can_claim: boolean;
    claim_gives_password: boolean;
    bot_user: string;
    cutover: boolean;
    is_admin: boolean;
};

export type ChannelStatus = {
    mapped: boolean;
    room_id?: string;
    room_name?: string;
    element_url?: string;
    direct: boolean;
    can_manage: boolean;
    can_create: boolean;
    calls_mode: string;
    can_call: boolean;
    cutover: boolean;
    mode: string;
    participants: number;
    encrypted?: boolean;
    encryption_works?: boolean;
};

export type Claim = {matrix_user_id: string; one_time_password: string};
export type LinkCode = {code: string; bot_user: string; command: string; expires_min: number};

const base = `/plugins/${manifest.id}/api/v1`;

function csrfToken(): string {
    const match = (/(?:^|;\s*)MMCSRF=([^;]+)/).exec(document.cookie);
    return match ? decodeURIComponent(match[1]) : '';
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = {'X-Requested-With': 'XMLHttpRequest'};
    if (method !== 'GET') {
        headers['X-CSRF-Token'] = csrfToken();
        headers['Content-Type'] = 'application/json';
    }
    const res = await fetch(base + path, {method, headers, credentials: 'include', body: body === undefined ? undefined : JSON.stringify(body)});
    const text = await res.text();
    let data: unknown = {};
    try {
        data = text ? JSON.parse(text) : {};
    } catch {
        data = {};
    }
    if (!res.ok) {
        const message = (data as {error?: string}).error;
        throw new Error(message || `Request failed (${res.status})`);
    }
    return data as T;
}

export const api = {
    me: () => request<Me>('GET', '/me'),
    channel: (channelId: string) => request<ChannelStatus>('GET', `/channels/${channelId}`),
    claim: () => request<Claim>('POST', '/me/claim'),
    startLink: () => request<LinkCode>('POST', '/me/link'),
    unlink: () => request<{ok: boolean}>('DELETE', '/me/link'),
    bridge: (channelId: string) => request<{message: string}>('POST', `/channels/${channelId}/bridge`),
    startCall: (channelId: string, title: string) => request<{join_url: string}>('POST', `/channels/${channelId}/call`, {title}),
};
