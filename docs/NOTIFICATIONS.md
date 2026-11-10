# Notifications across the bridge

What makes someone's phone buzz is decided by each side's own rules. The bridge
carries the signals those rules read; it cannot read or change the rules.

## What works

| Signal | Mattermost → Matrix | Matrix → Mattermost |
|---|---|---|
| Mention of a person | `@username` becomes a Matrix mention of that person's account (`m.mentions.user_ids` and a pill), so their Matrix push rules fire | A mention of a bridged person's account becomes `@username` in the post, so Mattermost notifies them as for any mention |
| Whole-channel mention | `@channel`, `@all` and `@here` set `m.mentions.room` | `@room` (the text or `m.mentions.room`) becomes `@channel` |
| Keywords | The post text is the same, so a person's own keyword rules on the receiving side match it | Same: the Mattermost post carries the text, so Mattermost's keyword highlights apply |
| Replies and threads | A reply to someone notifies them in Matrix as a reply does | A reply in a Matrix thread or reply is a reply in the Mattermost thread, with the replied-to text quoted |

## What cannot be done, and why

- **Per-person keyword rules do not travel.** Matrix keyword push rules live in
  each Matrix account; Mattermost's keywords are a setting of each Mattermost
  account. The plugin API gives no way to read or write either for another
  person, and mapping them would mean writing push rules into accounts the
  bridge does not own (it can for double-puppeted accounts, but does not).
- **Mute and notification levels are not shared.** Muting a channel in
  Mattermost does not mute the room in Matrix, and the other way round.
- **Do not disturb** and **status** are carried as presence when real-time
  events are on (see [OPERATIONS.md](OPERATIONS.md)), but they do not suppress
  the other side's notifications.
- **Email and mobile push** are sent by each side for its own users; the bridge
  adds nothing to them.

## What people should know

A person who uses both a Mattermost account and their own Matrix account (a
linked account, or a claimed mirror account) gets notified by both, because
both receive the message. Linking accounts (`/matrix link`) makes their messages
appear once, from the same identity on each side, but each client still
notifies.
