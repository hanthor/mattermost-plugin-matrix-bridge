// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

type CallPost = {
    message?: string;
    props?: Record<string, unknown>;
};

type Props = {
    post: CallPost;
};

// formatDuration renders a call length such as "12 min" or "1 h 5 min".
export function formatDuration(startAt: number, endAt: number): string {
    if (!startAt || !endAt || endAt < startAt) {
        return '';
    }
    const minutes = Math.max(1, Math.round((endAt - startAt) / 60000));
    if (minutes < 60) {
        return `${minutes} min`;
    }
    const hours = Math.floor(minutes / 60);
    const rest = minutes % 60;
    return rest ? `${hours} h ${rest} min` : `${hours} h`;
}

// safeJoinURL only lets http(s) links through: the URL comes from a message
// property that a Matrix user's client influences.
export function safeJoinURL(value: unknown): string {
    return typeof value === 'string' && (/^https?:\/\//i).test(value) ? value : '';
}

const cardStyle: React.CSSProperties = {
    border: '1px solid rgba(var(--center-channel-color-rgb), 0.16)',
    borderRadius: 8,
    padding: '12px 16px',
    margin: '4px 0',
    maxWidth: 420,
    display: 'flex',
    flexDirection: 'column',
    gap: 6,
};

const buttonStyle: React.CSSProperties = {
    alignSelf: 'flex-start',
    background: 'var(--button-bg)',
    color: 'var(--button-color)',
    borderRadius: 4,
    padding: '6px 14px',
    fontWeight: 600,
    textDecoration: 'none',
};

// MatrixCallCard renders a call that is running, or ran, in a Matrix room.
const MatrixCallCard = ({post}: Props) => {
    const p = post.props || {};
    const endAt = Number(p.end_at) || 0;
    const startAt = Number(p.start_at) || 0;
    const ended = endAt > 0;
    const participants = Array.isArray(p.participants) ? (p.participants as unknown[]).filter((n): n is string => typeof n === 'string') : [];
    const joinURL = safeJoinURL(p.join_url);
    const title = typeof p.title === 'string' && p.title ? p.title : 'Call in Matrix';

    return (
        <div
            className='matrix-call-card'
            data-testid='matrix-call-card'
            style={cardStyle}
        >
            <div style={{fontWeight: 600}}>
                {'📞 '}{title}
            </div>
            <div style={{opacity: 0.72}}>
                {ended ? `Call ended${formatDuration(startAt, endAt) ? ` · ${formatDuration(startAt, endAt)}` : ''}` : 'Call in progress'}
                {participants.length > 0 ? ` · ${participants.join(', ')}` : ''}
            </div>
            {!ended && joinURL && (

                // The URL is validated by safeJoinURL and the link is opened without a referrer.
                // eslint-disable-next-line @mattermost/use-external-link
                <a
                    href={joinURL}
                    target='_blank'
                    rel='noopener noreferrer'
                    style={buttonStyle}
                >
                    {'Join in Element'}
                </a>
            )}
        </div>
    );
};

export default MatrixCallCard;
