// Drives the Mattermost and Element web UIs against the stack deployed by
// TestWalkthrough and captures the screenshots used in docs/WALKTHROUGH.md.
//
// Mattermost is driven with Mattermost's own Playwright library
// (@mattermost/playwright-lib: API login, ChannelsPage, SystemConsolePage).
// Element is driven with helpers mirrored from element-web's Playwright
// suite (./element.mjs). Both sides are also poked through their REST APIs
// where a UI action is not what the tutorial is about.
//
// Input: WALKTHROUGH_SCENARIO (JSON) with URLs and credentials.
// Output: numbered PNGs plus manifest.json in scenario.outDir; on failures,
// a screenshot and the page HTML per step in the debug directory.

import {mkdirSync, writeFileSync} from 'node:fs';
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
const {chromium} = await import('@playwright/test');
const {TestBrowser, ChannelsPage, SystemConsolePage} = await import('@mattermost/playwright-lib');

mkdirSync(scenario.outDir, {recursive: true});
const debugDir = process.env.WALKTHROUGH_DEBUG_DIR || join(scenario.outDir, '..', 'walkthrough-debug');
mkdirSync(debugDir, {recursive: true});

const manifest = {generatedAtUTC: scenario.generatedAtUTC, steps: []};
let counter = 0;
const openPages = new Set();
const viewport = {width: 1440, height: 900};
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function shot(page, slug, caption) {
    counter += 1;
    const file = `${String(counter).padStart(2, '0')}-${slug}.png`;
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
    const res = await fetch(`${scenario.mattermostURL}/api/v4${path}`, {
        method,
        headers: {Authorization: `Bearer ${token}`, 'Content-Type': 'application/json'},
        body: body ? JSON.stringify(body) : undefined,
    });
    if (!res.ok) {
        throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`);
    }
    return res.json();
}

async function matrixApi(method, path, body, token = scenario.aliceToken) {
    const res = await fetch(`${scenario.homeserverURL}${path}`, {
        method,
        headers: {Authorization: `Bearer ${token}`, 'Content-Type': 'application/json'},
        body: body ? JSON.stringify(body) : undefined,
    });
    const text = await res.text();
    if (!res.ok) {
        throw new Error(`${method} ${path}: ${res.status} ${text}`);
    }
    return text ? JSON.parse(text) : {};
}

// ---------- Mattermost (via @mattermost/playwright-lib) ----------

const browser = await chromium.launch();

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
    return {
        context,
        page,
        channels: new ChannelsPage(page),
        systemConsole: new SystemConsolePage(page),
        async openChannel(channelName) {
            await this.channels.goto(scenario.teamName, channelName);
            await this.channels.centerView.postCreate.toBeVisible();
            await dismissMattermostTips(page);
        },
        async post(text) {
            await this.channels.postMessage(text);
            await this.channels.centerView.waitUntilLastPostContains(text, 30_000);
        },
        async slash(command) {
            const input = this.channels.centerView.postCreate.input;
            await this.channels.centerView.postCreate.writeMessage(command);
            await input.press('Enter');
            // The command autocomplete can swallow the first Enter.
            await sleep(500);
            if ((await input.inputValue()).trim() !== '') {
                await input.press('Enter');
            }
            await sleep(2500);
        },
        async close() {
            openPages.delete(page);
            await context.close();
        },
    };
}

async function dismissMattermostTips(page) {
    for (const label of [/skip for now/i, /^skip$/i, /not now/i]) {
        const button = page.getByRole('button', {name: label}).first();
        if (await button.isVisible().catch(() => false)) {
            await button.click().catch(() => {});
        }
    }
}

// ---------- Element ----------

async function elementSession() {
    const context = await browser.newContext({viewport, deviceScaleFactor: 2});
    const page = await context.newPage();
    openPages.add(page);
    await populateLocalStorageWithCredentials(page, {
        homeserverBaseUrl: scenario.homeserverURL,
        userId: scenario.aliceUserID,
        accessToken: scenario.aliceToken,
        deviceId: scenario.aliceDeviceID,
    });
    const app = new ElementAppPage(page, scenario.elementURL);
    await app.open();
    return {context, page, app};
}

// ---------- The tutorial ----------

const channelName = 'bridge-demo';
let admin;
let element;
let channelID = '';
let roomID = '';

await step('mattermost admin login', async () => {
    admin = await mattermostSession(scenario.adminUsername, scenario.adminPassword);
});

await step('plugin settings', async () => {
    await admin.page.goto(`/admin_console/plugins/plugin_${scenario.pluginID}`);
    await admin.systemConsole.sidebar.toBeVisible();
    await admin.page.getByText('Matrix Server URL', {exact: true}).first().waitFor({timeout: 60_000});
    await shot(admin.page, 'plugin-settings', 'System Console → Plugins → Mattermost bridge for Matrix: server URL, generated tokens, and the registration file download.');
});

await step('create channel and test connection', async () => {
    const team = await mmApi('GET', `/teams/name/${scenario.teamName}`);
    const channel = await mmApi('POST', '/channels', {
        team_id: team.id, name: channelName, display_name: 'Bridge Demo', type: 'O',
    });
    channelID = channel.id;
    await admin.openChannel(channelName);
    await admin.slash('/matrix test');
    await shot(admin.page, 'matrix-test', '`/matrix test` confirms the homeserver connection and appservice permissions.');
});

await step('create and map a room', async () => {
    await admin.slash('/matrix create');
    await shot(admin.page, 'matrix-create', '`/matrix create` creates a Matrix room for the channel and bridges it.');

    // /matrix list prints "channel → `!room`"; pick the room up from there.
    const list = await mmApi('POST', '/commands/execute', {channel_id: channelID, command: '/matrix list'});
    const match = /(![^\s`*]+)/.exec(list.text || '');
    if (!match) {
        throw new Error(`could not find the room in /matrix list output: ${list.text}`);
    }
    roomID = match[1];
    await matrixApi('POST', `/_matrix/client/v3/join/${encodeURIComponent(roomID)}`, {});
});

await step('message from mattermost', async () => {
    await admin.post('Hello Matrix! This message was written in **Mattermost**. :wave:');
    await shot(admin.page, 'mattermost-message', 'A message posted in the Mattermost channel.');
});

await step('element login and bridged message', async () => {
    element = await elementSession();
    await element.app.viewRoomById(roomID);
    await element.app.waitForMessage('This message was written in');
    await shot(element.page, 'element-bridged-message', 'The same message in Element, sent by the Mattermost user\'s ghost account with their name and avatar.');
});

await step('reply from matrix', async () => {
    await element.app.sendMessage('Hi from Matrix 👋 — replies, edits, reactions and files all flow back.');
    await admin.channels.centerView.waitUntilLastPostContains('Hi from Matrix', 60_000);
    await shot(admin.page, 'mattermost-bridged-reply', 'The Matrix reply appears in Mattermost as a remote user carrying the Matrix display name.');
});

await step('reaction from matrix', async () => {
    const messages = await matrixApi('GET', `/_matrix/client/v3/rooms/${encodeURIComponent(roomID)}/messages?dir=b&limit=20`);
    const target = (messages.chunk || []).find((ev) => ev.type === 'm.room.message' && (ev.content?.body || '').includes('written in'));
    if (!target) {
        throw new Error('bridged message not found in the Matrix room');
    }
    await matrixApi('PUT', `/_matrix/client/v3/rooms/${encodeURIComponent(roomID)}/send/m.reaction/${Date.now()}`, {
        'm.relates_to': {rel_type: 'm.annotation', event_id: target.event_id, key: '👍'},
    });
    await admin.page.locator('.post-reaction, .Reaction').first().waitFor({timeout: 60_000});
    await shot(admin.page, 'mattermost-reaction', 'A 👍 reaction added in Element shows up on the Mattermost post.');
});

await step('link identities', async () => {
    // Carol links her Mattermost account to alice's Matrix account.
    const carolUser = await mmApi('GET', `/users/username/${scenario.carolUsername}`);
    await mmApi('POST', `/channels/${channelID}/members`, {user_id: carolUser.id});
    const carol = await mattermostSession(scenario.carolUsername, scenario.carolPassword);
    await carol.openChannel(channelName);
    await carol.slash('/matrix link');
    await shot(carol.page, 'matrix-link', '`/matrix link` gives a one-time code to hand to the bridge bot in Matrix.');

    const codeMatch = /!mattermost link ([A-Z0-9]{8})/.exec(await carol.page.locator('body').innerText());
    if (!codeMatch) {
        throw new Error('link code not found on page');
    }

    // Alice talks to the bot in a DM.
    const dm = await matrixApi('POST', '/_matrix/client/v3/createRoom', {
        preset: 'private_chat', is_direct: true, invite: [scenario.bridgeBotUser],
    });
    await sleep(2000);
    await matrixApi('PUT', `/_matrix/client/v3/rooms/${encodeURIComponent(dm.room_id)}/send/m.room.message/${Date.now()}`, {
        msgtype: 'm.text', body: `!mattermost link ${codeMatch[1]}`,
    });
    await sleep(2500);
    await matrixApi('PUT', `/_matrix/client/v3/rooms/${encodeURIComponent(dm.room_id)}/send/m.room.message/${Date.now() + 1}`, {
        msgtype: 'm.text', body: `!mattermost login ${scenario.aliceToken}`,
    });
    await element.app.viewRoomById(dm.room_id);
    await element.app.waitForMessage('Double puppeting enabled');
    await shot(element.page, 'element-bot-dm', 'In Element, the bridge bot confirms the link and, after `!mattermost login`, double puppeting.');

    // Carol's next Mattermost message appears from alice's own Matrix account.
    await carol.post('This is Carol in Mattermost — but in Matrix it comes from my own account.');
    await element.app.viewRoomById(roomID);
    await element.app.waitForMessage('This is Carol in Mattermost');
    await shot(element.page, 'element-double-puppet', 'Carol\'s Mattermost message in Element, sent by her linked Matrix account rather than a ghost.');
    await carol.close();
});

await step('list mappings', async () => {
    await admin.openChannel(channelName);
    await admin.slash('/matrix list');
    await shot(admin.page, 'matrix-list', '`/matrix list` shows every bridged channel.');
});

await browser.close();
writeFileSync(join(scenario.outDir, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
console.log(`walkthrough complete: ${manifest.steps.filter((s) => s.file).length} screenshots`);
