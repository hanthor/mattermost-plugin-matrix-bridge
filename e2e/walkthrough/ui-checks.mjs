// Browser-level checks of what the Mattermost web client shows while Matrix
// users act, run by TestMattermostUI (E2E_UI=1). Each check is independent;
// results go to results.json in UI_OUT_DIR, with a screenshot and the page
// HTML for every failure.
//
// Input: UI_SCENARIO (JSON) with the URLs, the channel and room, Carol (the
// Mattermost user whose browser is driven) and Alice and Bob (Matrix users).

import {mkdirSync, writeFileSync} from 'node:fs';
import {join} from 'node:path';

import {chromium, expect} from '@playwright/test';

const scenario = JSON.parse(process.env.UI_SCENARIO || '{}');
const outDir = process.env.UI_OUT_DIR || '.';
mkdirSync(outDir, {recursive: true});
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const TIMEOUT = 60_000;

// ---------- Matrix helpers (REST) ----------

let txn = Date.now();

async function matrix(method, path, body, token) {
    const res = await fetch(`${scenario.homeserverURL}${path}`, {
        method,
        headers: {Authorization: `Bearer ${token}`, 'Content-Type': 'application/json'},
        body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await res.text();
    if (!res.ok) {
        throw new Error(`${method} ${path}: ${res.status} ${text}`);
    }
    return text ? JSON.parse(text) : {};
}

const room = encodeURIComponent(scenario.roomID);
const alice = scenario.alice.token;

async function send(type, content, token = alice) {
    txn += 1;
    return matrix('PUT', `/_matrix/client/v3/rooms/${room}/send/${type}/${txn}`, content, token);
}

const say = (body, extra = {}, token = alice) => send('m.room.message', {msgtype: 'm.text', body, ...extra}, token);

// A 1x1 PNG, the smallest real picture.
const PNG_1X1 = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64');

// ---------- Mattermost: sign in as Carol ----------

// Mattermost's own Playwright library does the sign-in.
process.env.PW_BASE_URL = scenario.mattermostURL;
process.env.PW_ADMIN_USERNAME = 'admin';
process.env.PW_ADMIN_PASSWORD = 'unused';
process.env.PW_ADMIN_EMAIL = 'admin@example.com';
process.env.PW_HEADLESS = 'true';
const {TestBrowser} = await import('@mattermost/playwright-lib');

// E2E_CHROMIUM points at a Chromium binary when the installed Playwright
// browsers do not match this package's Playwright version.
const browser = await chromium.launch(process.env.E2E_CHROMIUM ? {executablePath: process.env.E2E_CHROMIUM} : {});
const {context: loginContext} = await new TestBrowser(browser).login({username: scenario.carol.username, password: scenario.carol.password});
const storageState = await loginContext.storageState();
await loginContext.close();
const context = await browser.newContext({storageState, baseURL: scenario.mattermostURL, viewport: {width: 1440, height: 900}});
const page = await context.newPage();

async function dismissTips() {
    for (const label of [/skip for now/i, /^skip$/i, /not now/i, /^got it$/i]) {
        const button = page.getByRole('button', {name: label}).first();
        if (await button.isVisible().catch(() => false)) {
            await button.click().catch(() => {});
        }
    }
    const banner = page.locator('.announcement-bar__close').first();
    if (await banner.isVisible().catch(() => false)) {
        await banner.click().catch(() => {});
    }
}

async function open(channelName) {
    await page.goto(`${scenario.mattermostURL}/${scenario.teamName}/channels/${channelName}`);
    try {
        await page.locator('#post_textbox, [data-testid="post_textbox"]').first().waitFor({timeout: TIMEOUT});
    } catch (err) {
        await page.screenshot({path: join(outDir, 'open-failed.png')});
        writeFileSync(join(outDir, 'open-failed.html'), `${page.url()}\n\n${await page.content()}`);
        throw err;
    }
    await dismissTips();
    await sleep(500);
}

/** The newest post's message area. */
const lastText = () => page.locator('.post-message__text').last();

async function waitForPost(text) {
    await page.locator('.post-message__text', {hasText: text}).last().waitFor({timeout: TIMEOUT});
    return page.locator('.post', {has: page.locator('.post-message__text', {hasText: text})}).last();
}

// ---------- The checks ----------

const results = [];

async function check(name, fn) {
    try {
        await fn();
        results.push({name, ok: true});
        console.log(`ok   ${name}`);
    } catch (err) {
        results.push({name, ok: false, message: err.message});
        console.log(`FAIL ${name}: ${err.message}`);
        const slug = name.replace(/[^a-z0-9]+/gi, '-').slice(0, 60);
        try {
            await page.screenshot({path: join(outDir, `${slug}.png`)});
            writeFileSync(join(outDir, `${slug}.html`), await page.content());
        } catch {
            // the page may be gone
        }
    }
}

await open(scenario.channelName);

await check('formatting from Matrix renders as rich text', async () => {
    await say('bold italic code link\none\ntwo', {
        format: 'org.matrix.custom.html',
        formatted_body: '<strong>boldword</strong> <em>italword</em> <code>codeword</code> <a href="https://example.com/page">linkword</a><ul><li>first item</li><li>second item</li></ul>',
    });
    const post = await waitForPost('boldword');
    await expect(post.locator('strong', {hasText: 'boldword'})).toBeVisible();
    await expect(post.locator('em', {hasText: 'italword'})).toBeVisible();
    await expect(post.locator('code', {hasText: 'codeword'})).toBeVisible();
    await expect(post.locator('a[href="https://example.com/page"]', {hasText: 'linkword'})).toBeVisible();
    await expect(post.locator('.post-message__text li')).toHaveCount(2);
});

await check('the author is shown with a name and a picture', async () => {
    await say('who wrote this');
    const post = await waitForPost('who wrote this');
    await expect(post.locator('.post__header').first()).toContainText(/alice/i);
    // A person without a picture gets a letter avatar; either way something is shown.
    await expect(post.locator('.post__img, .Avatar, .profile-icon').first()).toBeVisible();
});

await check('a mention of Carol is highlighted', async () => {
    await say(`${scenario.carol.username}: are you there`, {
        format: 'org.matrix.custom.html',
        formatted_body: `<a href="https://matrix.to/#/${scenario.carol.ghost}">carol</a>: are you there`,
        'm.mentions': {user_ids: [scenario.carol.ghost]},
    });
    const post = await waitForPost('are you there');
    await expect(post.locator('.mention--highlight, a.mention-link').first()).toBeVisible();
});

let rootEventID = '';
await check('an edit shows the Edited marker', async () => {
    const first = await say('to be edited');
    rootEventID = first.event_id;
    await waitForPost('to be edited');
    await say('* now edited', {
        'm.new_content': {msgtype: 'm.text', body: 'now edited'},
        'm.relates_to': {rel_type: 'm.replace', event_id: first.event_id},
    });
    const post = await waitForPost('now edited');
    await expect(post.locator('.post-edited__indicator, [class*="edited"]').first()).toBeVisible();
});

await check('a reaction from Matrix shows under the post', async () => {
    await send('m.reaction', {'m.relates_to': {rel_type: 'm.annotation', event_id: rootEventID, key: '👍'}});
    const post = await waitForPost('now edited');
    await expect(post.locator('.post-reaction, [class*="Reaction"]').first()).toBeVisible({timeout: TIMEOUT});
});

await check('a threaded reply is counted on the root post', async () => {
    await say('a reply in the thread', {
        'm.relates_to': {rel_type: 'm.thread', event_id: rootEventID, is_falling_back: true, 'm.in_reply_to': {event_id: rootEventID}},
    });
    const post = await waitForPost('now edited');
    await expect(post.getByText(/1 reply/i).first()).toBeVisible({timeout: TIMEOUT});
});

await check('an image from Matrix shows in the channel', async () => {
    const up = await fetch(`${scenario.homeserverURL}/_matrix/media/v3/upload?filename=dot.png`, {
        method: 'POST', headers: {Authorization: `Bearer ${alice}`, 'Content-Type': 'image/png'}, body: PNG_1X1,
    });
    const {content_uri: mxc} = await up.json();
    await send('m.room.message', {msgtype: 'm.image', body: 'dot.png', url: mxc, info: {mimetype: 'image/png', w: 1, h: 1, size: PNG_1X1.length}});
    await expect(page.locator('.post img[src*="/api/v4/files/"], .file-preview img, .image-loaded').last()).toBeVisible({timeout: TIMEOUT});
});

await check('typing in Matrix shows a typing indicator', async () => {
    const typing = async () => matrix('PUT', `/_matrix/client/v3/rooms/${room}/typing/${encodeURIComponent(scenario.alice.userID)}`, {typing: true, timeout: 30_000}, alice);
    await typing();
    const timer = setInterval(() => typing().catch(() => {}), 3000);
    try {
        await expect(page.getByText(/is typing/i).first()).toBeVisible({timeout: TIMEOUT});
    } finally {
        clearInterval(timer);
        await matrix('PUT', `/_matrix/client/v3/rooms/${room}/typing/${encodeURIComponent(scenario.alice.userID)}`, {typing: false}, alice).catch(() => {});
    }
});

await check('a message deleted in Matrix disappears', async () => {
    const doomed = await say('this will vanish');
    await waitForPost('this will vanish');
    txn += 1;
    await matrix('PUT', `/_matrix/client/v3/rooms/${room}/redact/${doomed.event_id}/${txn}`, {}, alice);
    await expect(page.locator('.post-message__text', {hasText: 'this will vanish'})).toHaveCount(0, {timeout: TIMEOUT});
});

await check('the room topic becomes the channel header', async () => {
    await matrix('PUT', `/_matrix/client/v3/rooms/${room}/state/m.room.topic/`, {topic: 'Header set from Element'}, alice);
    await expect(page.locator('#channelHeaderDescription, .channel-header__description').first()).toContainText('Header set from Element', {timeout: TIMEOUT});
});

await check('someone joining from Matrix is announced', async () => {
    await matrix('POST', `/_matrix/client/v3/join/${room}`, {}, scenario.bob.token);
    await expect(page.locator('.post__body, .post-message__text').filter({hasText: /joined the channel/i}).last()).toBeVisible({timeout: TIMEOUT});
});

await check('a call from Matrix is a card with a join button', async () => {
    await matrix('PUT', `/_matrix/client/v3/rooms/${room}/state/m.rtc.member/${encodeURIComponent(`_${scenario.alice.userID}_UIDEVICE`)}`, {
        application: {type: 'm.call'}, member: {claimed_user_id: scenario.alice.userID},
    }, alice);
    const card = page.locator('[data-testid="matrix-call-card"]').last();
    await expect(card).toBeVisible({timeout: TIMEOUT});
    await expect(card).toContainText('Call in progress');
    await expect(card.locator('a[href^="http"]').first()).toBeVisible();
});

await check('unread messages in another channel show a badge', async () => {
    await open('town-square');
    await say(`${scenario.carol.username}: ping while you are away`, {
        format: 'org.matrix.custom.html',
        formatted_body: `<a href="https://matrix.to/#/${scenario.carol.ghost}">carol</a>: ping while you are away`,
        'm.mentions': {user_ids: [scenario.carol.ghost]},
    });
    const item = page.locator(`#sidebarItem_${scenario.channelName}`);
    await expect(item).toBeVisible({timeout: TIMEOUT});
    await expect(item.locator('.badge')).toBeVisible({timeout: TIMEOUT});
});

await check('the bridge panel shows the channel and the account', async () => {
    await open(scenario.channelName);
    await page.locator(`[id="app-bar-icon-${scenario.pluginID}"] [role="button"]`).first().click();
    const panel = page.locator('#rhsContainer, [data-testid="bridge-panel"]').first();
    await expect(panel.getByText('Bridged to Matrix')).toBeVisible({timeout: TIMEOUT});
    await expect(panel.getByText('Your Matrix account', {exact: true})).toBeVisible();
});

await browser.close();
writeFileSync(join(outDir, 'results.json'), JSON.stringify(results, null, 2));
const failed = results.filter((r) => !r.ok);
console.log(`${results.length - failed.length}/${results.length} browser checks passed`);
process.exit(failed.length ? 1 : 0);
