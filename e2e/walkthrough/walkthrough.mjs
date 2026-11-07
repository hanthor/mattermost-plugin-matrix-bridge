// Drives the Mattermost and Element web UIs against the stack deployed by
// TestWalkthrough and captures the screenshots used in docs/WALKTHROUGH.md:
// first the setup on both sides, then every bridged feature as it looks on
// both sides, with demo people (avatars and names) and demo content.
//
// Mattermost is driven with Mattermost's own Playwright library
// (@mattermost/playwright-lib: API login, ChannelsPage, SystemConsolePage).
// Element is driven with helpers mirrored from element-web's Playwright
// suite (./element.mjs). Actions that are not what a screenshot is about go
// through the REST APIs, so the pictures stay stable across UI releases.
//
// Input: WALKTHROUGH_SCENARIO (JSON) with URLs and credentials.
// Output: PNGs named after their step plus manifest.json in scenario.outDir;
// on failures, a screenshot and the page HTML per step in the debug directory.

import {mkdirSync, readdirSync, unlinkSync, writeFileSync} from 'node:fs';
import {join} from 'node:path';

import {ElementAppPage, populateLocalStorageWithCredentials} from './element.mjs';

const scenario = JSON.parse(process.env.WALKTHROUGH_SCENARIO || '{}');
if (!scenario.mattermostURL) {
    console.error('WALKTHROUGH_SCENARIO is required');
    process.exit(2);
}

// The Mattermost library reads its server settings from the environment.
process.env.PW_BASE_URL = scenario.mattermostURL;
process.env.PW_ADMIN_USERNAME = scenario.adminUsername;
process.env.PW_ADMIN_PASSWORD = scenario.adminPassword;
process.env.PW_ADMIN_EMAIL = scenario.adminEmail || 'admin@example.com';
process.env.PW_HEADLESS = 'true';
const {chromium, expect} = await import('@playwright/test');
const {TestBrowser, ChannelsPage, SystemConsolePage} = await import('@mattermost/playwright-lib');

mkdirSync(scenario.outDir, {recursive: true});
for (const old of readdirSync(scenario.outDir)) {
    if (old.endsWith('.png')) {
        unlinkSync(join(scenario.outDir, old));
    }
}
const debugDir = process.env.WALKTHROUGH_DEBUG_DIR || join(scenario.outDir, '..', 'walkthrough-debug');
mkdirSync(debugDir, {recursive: true});

const manifest = {generatedAtUTC: scenario.generatedAtUTC, steps: []};
const openPages = new Set();
const viewport = {width: 1440, height: 900};
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function shot(page, slug, caption) {
    const file = `${slug}.png`;
    await page.screenshot({path: join(scenario.outDir, file)});
    manifest.steps.push({file, caption});
    console.log(`📸 ${file} — ${caption}`);
}

async function step(name, fn) {
    try {
        await fn();
    } catch (err) {
        console.error(`step "${name}" failed: ${err.message}`);
        manifest.steps.push({error: name, message: err.message});
        const slug = name.replace(/[^a-z0-9]+/gi, '-');
        let i = 0;
        for (const page of openPages) {
            i += 1;
            try {
                await page.screenshot({path: join(debugDir, `${slug}-${i}.png`)});
                writeFileSync(join(debugDir, `${slug}-${i}.txt`), `${page.url()}\n\n${await page.content()}`);
            } catch {
                // the page may already be closed
            }
        }
    }
}

// ---------- REST helpers ----------

async function mmApi(method, path, body, token = scenario.adminToken) {
    const isForm = body instanceof FormData;
    const res = await fetch(`${scenario.mattermostURL}/api/v4${path}`, {
        method,
        headers: isForm ? {Authorization: `Bearer ${token}`} : {Authorization: `Bearer ${token}`, 'Content-Type': 'application/json'},
        body: isForm ? body : (body ? JSON.stringify(body) : undefined),
    });
    const text = await res.text();
    if (!res.ok) {
        throw new Error(`${method} ${path}: ${res.status} ${text}`);
    }
    return text ? JSON.parse(text) : {};
}

async function mmLogin(username, password) {
    const res = await fetch(`${scenario.mattermostURL}/api/v4/users/login`, {
        method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({login_id: username, password}),
    });
    if (!res.ok) {
        throw new Error(`login ${username}: ${res.status}`);
    }
    return res.headers.get('token');
}

async function matrixApi(method, path, body, token = scenario.alice.token, contentType = 'application/json') {
    const res = await fetch(`${scenario.homeserverURL}${path}`, {
        method,
        headers: {Authorization: `Bearer ${token}`, 'Content-Type': contentType},
        body: contentType === 'application/json' ? (body ? JSON.stringify(body) : undefined) : body,
    });
    const text = await res.text();
    if (!res.ok) {
        throw new Error(`${method} ${path}: ${res.status} ${text}`);
    }
    return text ? JSON.parse(text) : {};
}

let txn = Date.now();
async function matrixSend(roomID, type, content, token = scenario.alice.token) {
    txn += 1;
    return matrixApi('PUT', `/_matrix/client/v3/rooms/${encodeURIComponent(roomID)}/send/${type}/${txn}`, content, token);
}

async function matrixText(roomID, body, extra = {}, token = scenario.alice.token) {
    return matrixSend(roomID, 'm.room.message', {msgtype: 'm.text', body, ...extra}, token);
}

async function matrixUpload(png, filename, token = scenario.alice.token) {
    const res = await matrixApi('POST', `/_matrix/media/v3/upload?filename=${encodeURIComponent(filename)}`, png, token, 'image/png');
    return res.content_uri;
}

const localpart = (userID) => userID.slice(1).split(':')[0];

// ---------- Demo people: avatars and names ----------

const browser = await chromium.launch();

/** Renders an initials avatar (or a wider "image attachment") to PNG. */
async function renderPNG({text, subtitle = '', color, width = 256, height = 256}) {
    const context = await browser.newContext({viewport: {width, height}, deviceScaleFactor: 1});
    const page = await context.newPage();
    const fontSize = subtitle ? Math.round(height / 5) : Math.round(height / 2.2);
    await page.setContent(`<!doctype html><html><body style="margin:0">
      <div style="width:${width}px;height:${height}px;background:${color};display:flex;flex-direction:column;align-items:center;justify-content:center;
                  font-family:Inter,Helvetica,Arial,sans-serif;color:#fff">
        <div style="font-size:${fontSize}px;font-weight:700;letter-spacing:1px">${text}</div>
        ${subtitle ? `<div style="font-size:${Math.round(height / 11)}px;opacity:.85;margin-top:8px">${subtitle}</div>` : ''}
      </div></body></html>`);
    const png = await page.locator('div').first().screenshot({type: 'png'});
    await context.close();
    return png;
}

const people = {
    ada: {username: scenario.adminUsername, first: 'Ada', last: 'Admin', initials: 'AA', color: '#1c58d9'},
    carol: {username: scenario.carol.username, first: 'Carol', last: 'Carter', initials: 'CC', color: '#e07b00', id: scenario.carol.id},
    dave: {username: scenario.dave.username, first: 'Dave', last: 'Diaz', initials: 'DD', color: '#7a3db8', id: scenario.dave.id},
    alice: {userID: scenario.alice.userID, token: scenario.alice.token, name: 'Alice Anders', initials: 'AA', color: '#0dbd8b'},
    bob: {userID: scenario.bob.userID, token: scenario.bob.token, name: 'Bob Brandt', initials: 'BB', color: '#d9534f'},
};

async function setUpPeople() {
    // Show full names rather than usernames, as most teams configure it.
    await mmApi('PUT', '/config/patch', {TeamSettings: {TeammateNameDisplay: 'full_name'}});
    const adminUser = await mmApi('GET', `/users/username/${scenario.adminUsername}`);
    people.ada.id = adminUser.id;
    for (const key of ['ada', 'carol', 'dave']) {
        const p = people[key];
        await mmApi('PUT', `/users/${p.id}/patch`, {first_name: p.first, last_name: p.last, position: key === 'ada' ? 'Workspace admin' : 'Mattermost user'});
        const form = new FormData();
        form.append('image', new Blob([await renderPNG({text: p.initials, color: p.color})], {type: 'image/png'}), 'avatar.png');
        await mmApi('POST', `/users/${p.id}/image`, form);
    }
    for (const key of ['alice', 'bob']) {
        const p = people[key];
        const mxc = await matrixUpload(await renderPNG({text: p.initials, color: p.color}), 'avatar.png', p.token);
        await matrixApi('PUT', `/_matrix/client/v3/profile/${encodeURIComponent(p.userID)}/displayname`, {displayname: p.name}, p.token);
        await matrixApi('PUT', `/_matrix/client/v3/profile/${encodeURIComponent(p.userID)}/avatar_url`, {avatar_url: mxc}, p.token);
    }
}

// ---------- Mattermost (via @mattermost/playwright-lib) ----------

/**
 * Logs a user in with the library's API login, then moves the signed-in
 * storage state into a context with the tutorial's viewport and 2x scale.
 */
async function mattermostSession(username, password) {
    const testBrowser = new TestBrowser(browser);
    const {context: loginContext} = await testBrowser.login({username, password});
    const storageState = await loginContext.storageState();
    await loginContext.close();

    const context = await browser.newContext({
        storageState, baseURL: scenario.mattermostURL, viewport, deviceScaleFactor: 2,
    });
    const page = await context.newPage();
    openPages.add(page);
    const channels = new ChannelsPage(page);
    return {
        context,
        page,
        channels,
        systemConsole: new SystemConsolePage(page),
        async openChannel(name) {
            await channels.goto(scenario.teamName, name);
            await channels.centerView.postCreate.toBeVisible();
            await dismissMattermostTips(page);
            await sleep(1000);
        },
        /** Posts text and waits for it to render; waitFor is plain text to look for once markdown is rendered. */
        async post(text, waitFor = text) {
            await channels.postMessage(text);
            await this.waitForText(waitFor);
        },
        async waitForText(text, timeout = 60_000) {
            // Not "last post": a bridged event can land right after it.
            await channels.centerView.container.getByText(text, {exact: false}).last().waitFor({timeout});
            await sleep(750);
        },
        /** Runs a slash command through the send button, which the autocomplete cannot swallow. */
        async slash(command) {
            const postCreate = channels.centerView.postCreate;
            await postCreate.writeMessage(command);
            await page.keyboard.press('Escape');
            await postCreate.sendMessage();
            await expect(postCreate.input).toHaveValue('', {timeout: 10_000});
            await sleep(2500);
        },
        async close() {
            openPages.delete(page);
            await context.close();
        },
    };
}

async function dismissMattermostTips(page) {
    // The unlicensed server shows a "Preview Mode" announcement bar.
    const banner = page.locator('.announcement-bar__close').first();
    if (await banner.isVisible().catch(() => false)) {
        await banner.click().catch(() => {});
    }
    for (const label of [/skip for now/i, /^skip$/i, /not now/i]) {
        const button = page.getByRole('button', {name: label}).first();
        if (await button.isVisible().catch(() => false)) {
            await button.click().catch(() => {});
        }
    }
}

// ---------- Element ----------

async function elementSession(person) {
    const context = await browser.newContext({viewport, deviceScaleFactor: 2});
    const page = await context.newPage();
    openPages.add(page);
    await populateLocalStorageWithCredentials(page, {
        homeserverBaseUrl: scenario.homeserverURL,
        userId: person.userID,
        accessToken: person.token,
        deviceId: person.deviceID,
    });
    const app = new ElementAppPage(page, scenario.elementURL);
    await app.open();
    return {context, page, app, async close() {
        openPages.delete(page);
        await context.close();
    }};
}

// ---------- The tutorial ----------

const channelName = 'bridge-demo';
let ada;
let carol;
let alice;
let channelID = '';
let roomID = '';
let helloPostID = '';
let helloEventID = '';

await step('people', async () => {
    await setUpPeople();
});

// ===== Part 1: setup on the Mattermost side =====

await step('setup: mattermost login', async () => {
    ada = await mattermostSession(scenario.adminUsername, scenario.adminPassword);
});

await step('setup: plugin management', async () => {
    await ada.page.goto('/admin_console/plugins/plugin_management');
    await ada.systemConsole.sidebar.toBeVisible();
    const row = ada.page.getByText('Mattermost bridge for Matrix').first();
    await row.waitFor({timeout: 60_000});
    await row.scrollIntoViewIfNeeded();
    await dismissMattermostTips(ada.page);
    await sleep(1000);
    await shot(ada.page, 'setup-mm-plugin-management', 'System Console → Plugins → Plugin Management: upload the bridge bundle and enable it.');
});

await step('setup: plugin settings', async () => {
    await ada.page.goto(`/admin_console/plugins/plugin_${scenario.pluginID}`);
    await ada.systemConsole.sidebar.toBeVisible();
    await ada.page.getByText('Matrix Server URL', {exact: true}).first().waitFor({timeout: 60_000});
    await ada.page.getByRole('button', {name: 'Regenerate'}).first().waitFor({timeout: 30_000});
    await ada.page.waitForLoadState('networkidle');
    await dismissMattermostTips(ada.page);
    await sleep(1000);
    await shot(ada.page, 'setup-mm-plugin-settings', 'The bridge settings: homeserver URL and the two appservice tokens the plugin generated.');

    const download = ada.page.getByRole('button', {name: /download/i}).first();
    await download.scrollIntoViewIfNeeded();
    await sleep(500);
    await shot(ada.page, 'setup-mm-registration-download', 'Further down, the registration file for the homeserver is generated from these settings.');
});

await step('setup: test connection', async () => {
    await ada.openChannel('town-square');
    await ada.slash('/matrix test');
    await shot(ada.page, 'setup-mm-matrix-test', '`/matrix test` confirms the homeserver connection and the appservice permissions.');
});

await step('setup: bridge an existing channel', async () => {
    // Off-Topic existed before the plugin was enabled, so it has no room yet.
    // publish=true makes a public room, so Matrix users can join it directly.
    await ada.openChannel('off-topic');
    await ada.slash('/matrix create publish=true');
    await shot(ada.page, 'setup-mm-matrix-create', 'For a channel that existed before the bridge, `/matrix create publish=true` creates a public Matrix room and bridges it.');
});

await step('setup: new channel gets a room automatically', async () => {
    const team = await mmApi('GET', `/teams/name/${scenario.teamName}`);
    const channel = await mmApi('POST', '/channels', {
        team_id: team.id, name: channelName, display_name: 'Bridge Demo', type: 'O', purpose: 'Where Mattermost meets Matrix',
    });
    channelID = channel.id;

    // The plugin creates and maps a room for every new channel; wait for it.
    const deadline = Date.now() + 60_000;
    while (!roomID) {
        const list = await mmApi('POST', '/commands/execute', {channel_id: channelID, command: '/matrix list'});
        const match = /Bridge Demo → `(![^`]+)`/.exec(list.text || '');
        if (match) {
            roomID = match[1];
            break;
        }
        if (Date.now() > deadline) {
            throw new Error(`no room was created for the new channel: ${list.text}`);
        }
        await sleep(1000);
    }
    await mmApi('POST', `/channels/${channelID}/members`, {user_id: people.carol.id});
    await ada.openChannel(channelName);
    await ada.slash('/matrix status');
    await shot(ada.page, 'setup-mm-matrix-status', 'A channel created after the bridge was enabled gets a room automatically; `/matrix status` shows it.');
});

// ===== Part 1b: the Matrix side =====

await step('setup: element login', async () => {
    alice = await elementSession(scenario.alice);
});

await step('setup: element join room', async () => {
    await alice.page.goto(`${scenario.elementURL}/#/room/${roomID}`);
    await alice.page.locator('.mx_RoomPreviewBar').waitFor({timeout: 60_000});
    await sleep(1000);
    await shot(alice.page, 'setup-element-room-preview', 'In Element, a Matrix user opens the bridged room: the bridge bot created it, so it is a normal Matrix room.');
    await alice.app.viewRoomById(roomID);
    await alice.app.openMemberList();
    await shot(alice.page, 'setup-element-members', 'After joining, the member list shows the bridge bot and a ghost user for every channel member.');
    await alice.app.closeRightPanel();
    await ada.waitForText('joined the channel');
    await shot(ada.page, 'setup-mm-matrix-user-joined', 'Back in Mattermost, the Matrix user appears as a remote user who joined the channel.');
});

// ===== Part 2: features, both ways =====

await step('feature: formatted message to matrix', async () => {
    await ada.post(
        'Welcome to the **Bridge Demo**! Everything posted here is mirrored to Matrix: _formatting_, `code`, [links](https://matrix.org) and lists:\n- bridged in both directions\n- with your name and avatar',
        'Everything posted here is mirrored',
    );
    await shot(ada.page, 'feature-mm-message', 'Ada posts a formatted message in Mattermost.');
    const hello = await mmApi('GET', `/channels/${channelID}/posts?per_page=5`);
    helloPostID = hello.order.find((id) => hello.posts[id].message.includes('Bridge Demo'));
    await alice.app.viewRoomById(roomID);
    await alice.app.waitForMessage('Everything posted here is mirrored');
    await shot(alice.page, 'feature-element-message', 'The same message in Element, sent by Ada\'s ghost user with her name and avatar, formatting intact.');
});

await step('feature: formatted message to mattermost', async () => {
    await alice.app.sendMessage('Hello from **Matrix**! Replies, edits, reactions and files all flow back the other way. :tada:');
    await ada.waitForText('Hello from');
    await shot(ada.page, 'feature-mm-message-from-matrix', 'Alice\'s reply arrives in Mattermost as a remote user with her Matrix display name and avatar.');
});

await step('feature: threads', async () => {
    const carolToken = await mmLogin(scenario.carol.username, scenario.carol.password);
    await mmApi('POST', '/posts', {channel_id: channelID, root_id: helloPostID, message: 'Thread replies stay threaded on both sides.'}, carolToken);
    const messages = await matrixApi('GET', `/_matrix/client/v3/rooms/${encodeURIComponent(roomID)}/messages?dir=b&limit=30`);
    const target = (messages.chunk || []).find((ev) => ev.type === 'm.room.message' && (ev.content?.body || '').includes('Bridge Demo'));
    helloEventID = target.event_id;
    await matrixText(roomID, 'And a Matrix thread reply lands in the same Mattermost thread.', {
        'm.relates_to': {rel_type: 'm.thread', event_id: helloEventID, is_falling_back: true, 'm.in_reply_to': {event_id: helloEventID}},
    });
    await ada.waitForText('2 replies');
    const root = await ada.channels.centerView.getPostById(helloPostID);
    await root.openAThread();
    await ada.channels.sidebarRight.toBeVisible();
    await ada.page.locator('#rhsContainer').getByText('same Mattermost thread').waitFor({timeout: 60_000});
    await sleep(1000);
    await shot(ada.page, 'feature-mm-thread', 'A Mattermost thread with a reply from Carol and one from Alice on Matrix.');
    await ada.channels.sidebarRight.closeButton.click();
    await alice.app.viewRoomById(roomID);
    await alice.page.locator('.mx_ThreadSummary').first().waitFor({timeout: 60_000});
    await sleep(1000);
    await shot(alice.page, 'feature-element-thread', 'In Element the same conversation is a Matrix thread under Ada\'s message.');
});

await step('feature: edits', async () => {
    await mmApi('PUT', `/posts/${helloPostID}/patch`, {message: 'Welcome to the **Bridge Demo**! Everything posted here is mirrored to Matrix: _formatting_, `code`, [links](https://matrix.org) and lists:\n- bridged in both directions\n- with your name and avatar\n- **and edits**'});
    await alice.app.waitForMessage('and edits');
    await alice.page.locator('.mx_EventTile_edited').first().waitFor({timeout: 60_000});
    await shot(alice.page, 'feature-element-edit', 'Ada edits her post; Element shows the edited message with the "(edited)" marker.');
    const own = await matrixApi('GET', `/_matrix/client/v3/rooms/${encodeURIComponent(roomID)}/messages?dir=b&limit=30`);
    const mine = (own.chunk || []).find((ev) => ev.sender === scenario.alice.userID && (ev.content?.body || '').includes('Hello from'));
    const edited = 'Hello from **Matrix**! Replies, edits, reactions and files all flow back the other way, including edits. :tada:';
    await matrixText(roomID, `* ${edited}`, {
        'm.new_content': {msgtype: 'm.text', body: edited},
        'm.relates_to': {rel_type: 'm.replace', event_id: mine.event_id},
    });
    await ada.waitForText('including edits');
    await shot(ada.page, 'feature-mm-edit', 'Alice edits her Matrix message; Mattermost updates the post and marks it "Edited".');
});

await step('feature: reactions', async () => {
    const carolToken = await mmLogin(scenario.carol.username, scenario.carol.password);
    await mmApi('POST', '/reactions', {user_id: people.carol.id, post_id: helloPostID, emoji_name: 'tada'}, carolToken);
    await matrixSend(roomID, 'm.reaction', {'m.relates_to': {rel_type: 'm.annotation', event_id: helloEventID, key: '👍'}});
    await ada.page.locator('.post-reaction, .Reaction').nth(1).waitFor({timeout: 60_000});
    await sleep(1000);
    await shot(ada.page, 'feature-mm-reactions', 'Reactions from both networks on one post: Carol\'s 🎉 from Mattermost and Alice\'s 👍 from Matrix.');
    await alice.page.locator('.mx_ReactionsRow').first().waitFor({timeout: 60_000});
    await sleep(1000);
    await shot(alice.page, 'feature-element-reactions', 'The same reactions in Element.');
});

await step('feature: mentions', async () => {
    const carolGhost = `${scenario.ghostPrefix}${people.carol.id}:${scenario.serverName}`;
    await matrixText(roomID, 'Carol Carter: mentions turn into real mentions on the other side.', {
        format: 'org.matrix.custom.html',
        formatted_body: `<a href="https://matrix.to/#/${carolGhost}">Carol Carter</a>: mentions turn into real mentions on the other side.`,
        'm.mentions': {user_ids: [carolGhost]},
    });
    await ada.waitForText('real mentions');
    await shot(ada.page, 'feature-mm-mention', 'Alice mentions Carol\'s ghost in Element; Mattermost renders it as an @carol mention.');
    await ada.post(`@matrix:${localpart(scenario.alice.userID)} and the other way round, a Mattermost mention becomes a Matrix pill.`, 'other way round');
    await alice.app.waitForMessage('other way round');
    await shot(alice.page, 'feature-element-mention', 'Ada mentions Alice\'s remote account in Mattermost; Element shows a pill for Alice.');
});

await step('feature: attachments', async () => {
    const chart = await renderPNG({text: 'Q3 numbers', subtitle: 'bridged-attachment.png · sent from Mattermost', color: '#1c58d9', width: 640, height: 360});
    const form = new FormData();
    form.append('channel_id', channelID);
    form.append('files', new Blob([chart], {type: 'image/png'}), 'bridged-attachment.png');
    const upload = await mmApi('POST', '/files', form);
    await mmApi('POST', '/posts', {channel_id: channelID, message: 'Images and files are re-uploaded on the other side.', file_ids: upload.file_infos.map((f) => f.id)});
    await alice.app.waitForMessage('Images and files');
    await alice.page.locator('.mx_MImageBody').first().waitFor({timeout: 60_000});
    await sleep(1500);
    await shot(alice.page, 'feature-element-attachment', 'An image attached in Mattermost arrives in Element as an m.image event.');

    const photo = await renderPNG({text: 'Hello Mattermost', subtitle: 'from-matrix.png · sent from Element', color: '#0dbd8b', width: 640, height: 360});
    const mxc = await matrixUpload(photo, 'from-matrix.png');
    await matrixSend(roomID, 'm.room.message', {msgtype: 'm.image', body: 'from-matrix.png', url: mxc, info: {mimetype: 'image/png', w: 640, h: 360, size: photo.length}});
    await ada.waitForText('from-matrix.png');
    await sleep(2000);
    await shot(ada.page, 'feature-mm-attachment', 'An image sent from Element shows up as a file attachment in Mattermost.');
});

await step('feature: typing', async () => {
    await matrixApi('PUT', `/_matrix/client/v3/rooms/${encodeURIComponent(roomID)}/typing/${encodeURIComponent(scenario.alice.userID)}`, {typing: true, timeout: 30_000});
    await ada.page.getByText(/is typing/).first().waitFor({timeout: 30_000});
    await shot(ada.page, 'feature-mm-typing', 'Typing in Element shows a typing indicator in Mattermost (appservice ephemeral events).');
    await matrixApi('PUT', `/_matrix/client/v3/rooms/${encodeURIComponent(roomID)}/typing/${encodeURIComponent(scenario.alice.userID)}`, {typing: false});
});

await step('feature: room topic', async () => {
    // The bridge bot created the room, so it hands Alice moderator power first
    // (what a room admin would do in Element's room settings).
    const asBot = `?user_id=${encodeURIComponent(scenario.bridgeBotUser)}`;
    const levels = await matrixApi('GET', `/_matrix/client/v3/rooms/${encodeURIComponent(roomID)}/state/m.room.power_levels/${asBot}`, undefined, scenario.asToken);
    levels.users = {...(levels.users || {}), [scenario.alice.userID]: 50};
    await matrixApi('PUT', `/_matrix/client/v3/rooms/${encodeURIComponent(roomID)}/state/m.room.power_levels/${asBot}`, levels, scenario.asToken);
    await matrixApi('PUT', `/_matrix/client/v3/rooms/${encodeURIComponent(roomID)}/state/m.room.topic/`, {topic: 'Where Mattermost meets Matrix — topic set from Element'});
    await ada.page.getByText('topic set from Element').first().waitFor({timeout: 60_000});
    await sleep(1000);
    await shot(ada.page, 'feature-mm-topic', 'Alice changes the room topic in Matrix; the Mattermost channel header follows.');
    await alice.app.viewRoomById(roomID);
    await sleep(1000);
    await shot(alice.page, 'feature-element-topic', 'The room in Element with the new topic.');
});

await step('feature: membership', async () => {
    await mmApi('POST', `/channels/${channelID}/members`, {user_id: people.dave.id});
    await matrixApi('POST', `/_matrix/client/v3/join/${encodeURIComponent(roomID)}`, {}, scenario.bob.token);
    await ada.waitForText(`@matrix:${localpart(scenario.bob.userID)}`);
    await shot(ada.page, 'feature-mm-membership', 'Dave is added to the channel and Bob joins from Matrix: both show up on both sides.');
    await alice.app.viewRoomById(roomID);
    await alice.app.waitForMessage('joined the room');
    await alice.app.openMemberList();
    await shot(alice.page, 'feature-element-membership', 'Element\'s member list: the bridge bot, Matrix users, and a ghost for each Mattermost member.');
    await alice.app.closeRightPanel();
});

await step('feature: identity link and double puppeting', async () => {
    carol = await mattermostSession(scenario.carol.username, scenario.carol.password);
    await carol.openChannel(channelName);
    await carol.slash('/matrix link');
    await shot(carol.page, 'feature-mm-matrix-link', '`/matrix link` gives Carol a one-time code for the bridge bot in Matrix.');

    const codeMatch = /!mattermost link ([A-Z0-9]{8})/.exec(await carol.page.locator('body').innerText());
    if (!codeMatch) {
        throw new Error('link code not found on page');
    }
    const dm = await matrixApi('POST', '/_matrix/client/v3/createRoom', {preset: 'private_chat', is_direct: true, invite: [scenario.bridgeBotUser]});
    await sleep(2000);
    await matrixText(dm.room_id, `!mattermost link ${codeMatch[1]}`);
    await sleep(2500);
    await matrixText(dm.room_id, `!mattermost login ${scenario.alice.token}`);
    await alice.app.viewRoomById(dm.room_id);
    await alice.app.waitForMessage('Double puppeting enabled');
    await shot(alice.page, 'feature-element-bot-dm', 'In Element, the bridge bot confirms the link and, after `!mattermost login`, double puppeting.');

    await carol.post('Now my Mattermost messages come from my own Matrix account instead of a ghost.', 'my own Matrix account');
    await alice.app.viewRoomById(roomID);
    await alice.app.waitForMessage('my own Matrix account');
    await shot(alice.page, 'feature-element-double-puppet', 'Carol\'s Mattermost message in Element, sent by her linked Matrix account rather than a ghost.');
});

await step('operations: status and list', async () => {
    await ada.openChannel(channelName);
    await ada.slash('/matrix status');
    await shot(ada.page, 'ops-mm-matrix-status', '`/matrix status` shows the bridge state of the current channel.');
    await ada.slash('/matrix list');
    await shot(ada.page, 'ops-mm-matrix-list', '`/matrix list` shows every bridged channel in the team.');
});

await browser.close();
writeFileSync(join(scenario.outDir, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
console.log(`walkthrough complete: ${manifest.steps.filter((s) => s.file).length} screenshots, ${manifest.steps.filter((s) => s.error).length} failed steps`);
