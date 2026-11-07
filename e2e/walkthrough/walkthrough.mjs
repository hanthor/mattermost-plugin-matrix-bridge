// Drives the Mattermost and Element web UIs against the stack deployed by
// TestWalkthrough and captures the screenshots used in docs/WALKTHROUGH.md.
//
// Input: WALKTHROUGH_SCENARIO (JSON) with URLs and credentials.
// Output: numbered PNGs plus manifest.json in scenario.outDir.
//
// Every step is best-effort: a failed step logs the error and the run goes
// on, so a UI change in one place does not hide the rest of the tutorial.

import {chromium} from 'playwright';
import {mkdirSync, writeFileSync} from 'node:fs';
import {join} from 'node:path';

const scenario = JSON.parse(process.env.WALKTHROUGH_SCENARIO || '{}');
if (!scenario.mattermostURL) {
    console.error('WALKTHROUGH_SCENARIO is required');
    process.exit(2);
}

mkdirSync(scenario.outDir, {recursive: true});
const manifest = {generatedAtUTC: scenario.generatedAtUTC, steps: []};
let counter = 0;

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function shot(page, slug, caption) {
    counter += 1;
    const file = `${String(counter).padStart(2, '0')}-${slug}.png`;
    await page.screenshot({path: join(scenario.outDir, file), fullPage: false});
    manifest.steps.push({file, caption});
    console.log(`📸 ${file} — ${caption}`);
    return file;
}

async function step(name, fn) {
    try {
        await fn();
    } catch (err) {
        console.error(`step "${name}" failed: ${err.message}`);
        manifest.steps.push({error: name, message: err.message});
    }
}

// ---------- Mattermost helpers ----------

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

async function mmLogin(page, username, password) {
    await page.goto(`${scenario.mattermostURL}/login`);
    await page.getByPlaceholder(/email|username/i).first().fill(username);
    await page.locator('#input_password-input, input[type=password]').first().fill(password);
    await page.getByRole('button', {name: /log in/i}).click();
    await page.waitForURL((url) => !url.pathname.includes('/login'), {timeout: 60_000});
    await dismissMattermostTips(page);
}

async function dismissMattermostTips(page) {
    for (const label of [/skip for now/i, /^skip$/i, /not now/i, /close/i]) {
        const button = page.getByRole('button', {name: label}).first();
        if (await button.isVisible().catch(() => false)) {
            await button.click().catch(() => {});
        }
    }
}

async function mmPost(page, text) {
    const box = page.locator('#post_textbox');
    await box.click();
    await box.fill(text);
    await box.press('Enter');
    await sleep(1500);
}

async function mmSlash(page, command) {
    const box = page.locator('#post_textbox');
    await box.click();
    await box.fill(command);
    await box.press('Enter');
    // Slash command autocomplete may swallow the first Enter.
    await sleep(500);
    if ((await box.inputValue()).trim() !== '') {
        await box.press('Enter');
    }
    await sleep(2500);
}

// ---------- Matrix helpers ----------

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

async function elementLogin(page) {
    await page.goto(`${scenario.elementURL}/#/login`);
    const localpart = scenario.aliceUserID.slice(1).split(':')[0];
    await page.locator('#mx_LoginForm_username, input[name=username]').first().fill(localpart);
    await page.locator('#mx_LoginForm_password, input[name=password]').first().fill(scenario.alicePassword);
    await page.getByRole('button', {name: /sign in/i}).click();
    await page.waitForURL((url) => url.hash.startsWith('#/home') || url.hash.startsWith('#/room'), {timeout: 90_000});
    await dismissElementDialogs(page);
}

async function dismissElementDialogs(page) {
    for (const label of [/skip/i, /dismiss/i, /close/i, /not now/i, /ok/i]) {
        const button = page.getByRole('button', {name: label}).first();
        if (await button.isVisible().catch(() => false)) {
            await button.click().catch(() => {});
        }
    }
}

async function elementOpenRoom(page, roomID) {
    await page.goto(`${scenario.elementURL}/#/room/${encodeURIComponent(roomID)}`);
    await page.locator('.mx_BasicMessageComposer_input, [contenteditable=true]').first().waitFor({timeout: 60_000});
    await sleep(1500);
}

async function elementSend(page, text) {
    const composer = page.locator('.mx_BasicMessageComposer_input, [contenteditable=true]').first();
    await composer.click();
    await composer.fill(text);
    await composer.press('Enter');
    await sleep(2000);
}

// ---------- The tutorial ----------

const browser = await chromium.launch();
const mmContext = await browser.newContext({viewport: {width: 1440, height: 900}, deviceScaleFactor: 2});
const elementContext = await browser.newContext({viewport: {width: 1440, height: 900}, deviceScaleFactor: 2});
const mm = await mmContext.newPage();
const element = await elementContext.newPage();

const channelName = 'bridge-demo';
let channelID = '';
let roomID = '';

await step('mattermost admin login', async () => {
    await mmLogin(mm, scenario.adminUsername, scenario.adminPassword);
});

await step('plugin settings', async () => {
    await mm.goto(`${scenario.mattermostURL}/admin_console/plugins/plugin_${scenario.pluginID}`);
    await mm.getByText(/Matrix Server URL/i).first().waitFor({timeout: 60_000});
    await shot(mm, 'plugin-settings', 'System Console → Plugins → Mattermost bridge for Matrix: server URL, generated tokens, and the registration file download.');
});

await step('create channel and test connection', async () => {
    const team = await mmApi('GET', `/teams/name/${scenario.teamName}`);
    const channel = await mmApi('POST', '/channels', {
        team_id: team.id, name: channelName, display_name: 'Bridge Demo', type: 'O',
    });
    channelID = channel.id;
    await mm.goto(`${scenario.mattermostURL}/${scenario.teamName}/channels/${channelName}`);
    await mm.locator('#post_textbox').waitFor({timeout: 60_000});
    await dismissMattermostTips(mm);
    await mmSlash(mm, '/matrix test');
    await shot(mm, 'matrix-test', '`/matrix test` confirms the homeserver connection and appservice permissions.');
});

await step('create and map a room', async () => {
    await mmSlash(mm, '/matrix create');
    await shot(mm, 'matrix-create', '`/matrix create` creates a Matrix room for the channel and bridges it.');

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
    await mmPost(mm, 'Hello Matrix! This message was written in **Mattermost**. :wave:');
    await shot(mm, 'mattermost-message', 'A message posted in the Mattermost channel.');
});

await step('element login and bridged message', async () => {
    await elementLogin(element);
    await elementOpenRoom(element, roomID);
    await element.getByText('This message was written in').first().waitFor({timeout: 60_000});
    await shot(element, 'element-bridged-message', 'The same message in Element, sent by the Mattermost user\'s ghost account with their name and avatar.');
});

await step('reply from matrix', async () => {
    await elementSend(element, 'Hi from Matrix 👋 — replies, edits, reactions and files all flow back.');
    await mm.bringToFront();
    await mm.getByText('Hi from Matrix').first().waitFor({timeout: 60_000});
    await shot(mm, 'mattermost-bridged-reply', 'The Matrix reply appears in Mattermost as a remote user carrying the Matrix display name.');
});

await step('reaction from matrix', async () => {
    const sync = await matrixApi('GET', `/_matrix/client/v3/rooms/${encodeURIComponent(roomID)}/messages?dir=b&limit=20`);
    const target = (sync.chunk || []).find((ev) => ev.type === 'm.room.message' && (ev.content?.body || '').includes('written in'));
    if (target) {
        await matrixApi('PUT', `/_matrix/client/v3/rooms/${encodeURIComponent(roomID)}/send/m.reaction/${Date.now()}`, {
            'm.relates_to': {rel_type: 'm.annotation', event_id: target.event_id, key: '👍'},
        });
        await sleep(3000);
        await shot(mm, 'mattermost-reaction', 'A 👍 reaction added in Element shows up on the Mattermost post.');
    }
});

await step('link identities', async () => {
    // Carol links her Mattermost account to alice's Matrix account.
    const carolContext = await browser.newContext({viewport: {width: 1440, height: 900}, deviceScaleFactor: 2});
    const carolPage = await carolContext.newPage();
    await mmLogin(carolPage, scenario.carolUsername, scenario.carolPassword);
    await mmApi('POST', `/channels/${channelID}/members`, {user_id: (await mmApi('GET', `/users/username/${scenario.carolUsername}`)).id});
    await carolPage.goto(`${scenario.mattermostURL}/${scenario.teamName}/channels/${channelName}`);
    await carolPage.locator('#post_textbox').waitFor({timeout: 60_000});
    await dismissMattermostTips(carolPage);
    await mmSlash(carolPage, '/matrix link');
    await shot(carolPage, 'matrix-link', '`/matrix link` gives a one-time code to hand to the bridge bot in Matrix.');

    const codeMatch = /!mattermost link ([A-Z0-9]{8})/.exec(await carolPage.locator('body').innerText());
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
    await sleep(2500);
    await elementOpenRoom(element, dm.room_id);
    await element.getByText(/Linked to Mattermost user/i).first().waitFor({timeout: 60_000});
    await shot(element, 'element-bot-dm', 'In Element, the bridge bot confirms the link and, after `!mattermost login`, double puppeting.');

    // Carol's next Mattermost message appears from alice's own Matrix account.
    await mmPost(carolPage, 'This is Carol in Mattermost — but in Matrix it comes from my own account.');
    await elementOpenRoom(element, roomID);
    await element.getByText('This is Carol in Mattermost').first().waitFor({timeout: 60_000});
    await shot(element, 'element-double-puppet', 'Carol\'s Mattermost message in Element, sent by her linked Matrix account rather than a ghost.');
    await carolContext.close();
});

await step('capabilities', async () => {
    await mm.bringToFront();
    await mmSlash(mm, '/matrix list');
    await shot(mm, 'matrix-list', '`/matrix list` shows every bridged channel.');
});

await browser.close();
writeFileSync(join(scenario.outDir, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
console.log(`walkthrough complete: ${manifest.steps.filter((s) => s.file).length} screenshots`);
