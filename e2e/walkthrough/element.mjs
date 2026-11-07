// Element Web page helpers for the walkthrough.
//
// Element does not publish its app page objects, so these mirror the relevant
// pieces of element-web's own Playwright suite (apps/web/playwright/pages and
// packages/playwright-common): session injection through localStorage, toast
// handling, and the composer and timeline selectors.

/** Seeds the Element session so the app starts logged in. */
export async function populateLocalStorageWithCredentials(page, credentials) {
    await page.addInitScript(({credentials}) => {
        window.localStorage.setItem('mx_hs_url', credentials.homeserverBaseUrl);
        window.localStorage.setItem('mx_user_id', credentials.userId);
        window.localStorage.setItem('mx_access_token', credentials.accessToken);
        window.localStorage.setItem('mx_device_id', credentials.deviceId);
        window.localStorage.setItem('mx_is_guest', 'false');
        window.localStorage.setItem('mx_has_pickle_key', 'false');
        window.localStorage.setItem('mx_has_access_token', 'true');
        window.localStorage.setItem('mx_local_settings', JSON.stringify({
            ...JSON.parse(window.localStorage.getItem('mx_local_settings') ?? '{}'),
            language: 'en',
            theme: 'light',
        }));
    }, {credentials});
}

/** Clicks the secondary ("Later"/"Dismiss") button of a toast when it is showing. */
export async function rejectToastIfExists(page, title, timeout = 2000) {
    const toast = page.locator('.mx_Toast_toast', {hasText: title}).first();
    try {
        await toast.waitFor({state: 'visible', timeout});
    } catch {
        return false;
    }
    await toast.locator('.mx_Toast_buttons .mx_AccessibleButton_kind_danger_outline, .mx_Toast_buttons .mx_AccessibleButton_kind_primary_outline, .mx_Toast_buttons button').first().click();
    return true;
}

export async function dismissToasts(page) {
    await rejectToastIfExists(page, 'Verify this device', 300);
    if (await rejectToastIfExists(page, 'Turn on key storage', 300)) {
        const confirm = page.getByRole('button', {name: 'Continue'});
        if (await confirm.isVisible().catch(() => false)) {
            await confirm.click().catch(() => {});
        }
    }
    await rejectToastIfExists(page, 'Notifications', 300);
}

export class ElementAppPage {
    constructor(page, baseURL) {
        this.page = page;
        this.baseURL = baseURL;
    }

    async open() {
        await this.page.goto(`${this.baseURL}/#/home`);
        await this.page.waitForSelector('.mx_MatrixChat', {timeout: 60_000});
        await dismissToasts(this.page);
    }

    async viewRoomById(roomId) {
        await this.page.goto(`${this.baseURL}/#/room/${roomId}`);
        await dismissToasts(this.page);
        const join = this.page.locator('.mx_RoomPreviewBar').getByRole('button', {name: /join/i}).first();
        if (await join.isVisible().catch(() => false)) {
            await join.click();
        }
        await this.getComposerField().waitFor({timeout: 60_000});
        await dismissToasts(this.page);
    }

    getComposer() {
        return this.page.locator('.mx_RoomView_body .mx_MessageComposer');
    }

    getComposerField() {
        return this.getComposer().locator('div[contenteditable]');
    }

    async sendMessage(text) {
        const field = this.getComposerField();
        await field.click();
        await field.fill(text);
        await field.press('Enter');
    }

    /** Waits for an event tile whose body contains the text. */
    async waitForMessage(text, timeout = 60_000) {
        const tile = this.page.locator('.mx_RoomView_MessageList .mx_EventTile', {hasText: text}).last();
        await tile.waitFor({timeout});
        return tile;
    }
}
