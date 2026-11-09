# Shared login with Matrix Authentication Service and OpenID Connect

For a migration to feel seamless, a person should sign in to Matrix with the
same login they use for Mattermost, and land on the Matrix account the bridge
already made for them in [mirror mode](MIRROR_MODE_SETUP.md). This guide sets
that up with [Matrix Authentication Service](https://github.com/element-hq/matrix-authentication-service)
(MAS) and an OpenID Connect (OIDC) identity provider.

## How it fits together

The recommended shape is one OpenID Connect provider that both sides trust.
(If your people sign in to Mattermost with their own Mattermost passwords and
you have no separate provider, Mattermost itself can be the provider; see
[Mattermost as the provider](#mattermost-as-the-provider) at the end.)

```
                     ┌────────────────────┐
   people sign in ──▶│ OIDC identity      │◀── people sign in
   to Mattermost     │ provider (Keycloak,│    to Matrix (Element)
          │          │ Authentik, Entra…) │          │
          ▼          └────────────────────┘          ▼
   ┌────────────┐          same person            ┌─────┐   delegates   ┌─────────┐
   │ Mattermost │  (same `sub`, same username)    │ MAS │ ────────────▶ │ Synapse │
   └─────┬──────┘                                 └──▲──┘   sign-in     └────▲────┘
         │ bridge plugin                              │ admin API             │ appservice
         └────────────────────────────────────────────┴───────────────────────┘
```

- Mattermost signs people in with the provider (**OpenID Connect**, a
  Mattermost Professional or Enterprise feature) and records the provider's
  subject (`sub`) for each person.
- MAS is Synapse's sign-in service and uses the same provider as an **upstream**
  provider.
- The bridge, in mirror mode, creates each person's Matrix account `@username`.
  With MAS it also creates the account **in MAS** and **links it to the
  person's subject at the provider**. When they later sign in to Element with
  the provider, MAS recognises them and signs them in to that account. No
  password, no matching on names, nothing to guess.

Everyone who is not signed in to Mattermost with OpenID Connect (local
passwords, LDAP, GitLab…) still gets an account, but has no subject to link, so
they need the name-based fallback at the end of this guide.

## Why the bridge has to tell MAS

The bridge creates mirror accounts in Synapse through its appservice. MAS does
not know about accounts it did not create, and refuses to create a user whose
name Synapse already has (`409`: *the username is reserved by the homeserver*).
Without help, the first sign-in of every migrated person would fail. MAS offers
an admin API for this: create a user with `skip_homeserver_check` (meant for
users an application service uses), then link it to the upstream provider
subject. That is what the plugin does when the MAS settings below are filled in.

## 1. The identity provider

You need one OIDC client for MAS (Mattermost already has its own). Create a
confidential client with:

- **Redirect URI:** `https://mas.example.com/upstream/callback/<provider id>`
  (the provider ID is the ULID you choose in step 2),
- scopes `openid profile email`,
- `preferred_username` (or whichever claim you use) release for each person.

Make sure Mattermost and MAS see the **same** username: Mattermost's username
becomes the Matrix localpart, and MAS must import the same claim as the
localpart. Using `preferred_username` for both is the simple choice.

## 2. MAS

`config.yaml` of MAS (the relevant parts; see the
[MAS documentation](https://element-hq.github.io/matrix-authentication-service/)
for the rest):

```yaml
http:
  public_base: https://mas.example.com/

matrix:
  kind: synapse
  homeserver: example.com               # Synapse's server_name
  endpoint: http://synapse:8008/
  secret: "<a long random string, also used in Synapse below>"

# Sign in only through the identity provider.
passwords:
  enabled: false

# The client the bridge uses to talk to MAS.
clients:
  - client_id: 01J44QC8BCY7FCFM7WGHQGKMTJ        # a ULID of your choice
    client_auth_method: client_secret_basic
    client_secret: "<random secret for the bridge>"

policy:
  data:
    admin_clients:
      - 01J44QC8BCY7FCFM7WGHQGKMTJ

upstream_oauth2:
  providers:
    - id: 01HQW90Z35CMXFJWQPHC3BGZGQ              # a ULID of your choice
      human_name: "Company login"
      issuer: https://idp.example.com/realms/company
      client_id: "<the provider client for MAS>"
      client_secret: "<its secret>"
      scope: "openid profile email"
      claims_imports:
        localpart:
          action: require
          template: "{{ user.preferred_username }}"
          # The bridge links accounts itself, so nothing is matched by name here.
          on_conflict: fail
        displayname:
          action: suggest
          template: "{{ user.name }}"
        email:
          action: suggest
          template: "{{ user.email }}"
```

The `clients` entry plus `admin_clients` lets the bridge use MAS's admin API
with the client credentials grant (scope `urn:mas:admin`).

## 3. Synapse

Hand sign-in to MAS in `homeserver.yaml` (and keep the bridge's appservice
registration exactly as in the [mirror mode guide](MIRROR_MODE_SETUP.md)):

```yaml
matrix_authentication_service:
  enabled: true
  endpoint: http://mas:8080/
  secret: "<the same string as matrix.secret in MAS>"
```

The bridge registers its accounts with `inhibit_login: true`, which is what the
Matrix specification requires of application services, so it keeps working with
MAS in front of Synapse.

## 4. The bridge

In **System Console → Plugins → Mattermost bridge for Matrix**, next to **Enable
Mirror Mode**:

| Setting | Value |
| --- | --- |
| Matrix Authentication Service URL | `https://mas.example.com` (an address the Mattermost server can reach) |
| MAS Admin Client ID | the `client_id` above |
| MAS Admin Client Secret | its secret |
| MAS Identity Provider ID | the upstream provider's `id` (the ULID) |

Leave **Matrix Admin Access Token** empty: with MAS, Synapse no longer owns
passwords, so `/matrix claim` does not set one. It marks the account as claimed
and tells the person to sign in with the company login.

## What happens

1. A Mattermost person is bridged for the first time. The bridge registers
   `@username` in Synapse, creates it in MAS, and, if they signed in to
   Mattermost with OpenID Connect, links it to their provider subject.
2. The person opens Element and chooses **Continue with Company login**. MAS
   recognises their subject and signs them in to `@username`.
3. They type a message. The bridge sees a message from an account it did not
   send as, treats the account as claimed, and from then on their messages post
   in Mattermost as them (see *Claiming accounts* in the
   [mirror mode guide](MIRROR_MODE_SETUP.md#54-claiming-accounts)).

If MAS is down when an account is created, the account still works for the
bridge and the MAS part is retried the next time the account is used.

## Name-based fallback

For people without an OpenID Connect subject, MAS can link by matching the
localpart instead. In the provider's `claims_imports.localpart`, change
`on_conflict: fail` to `on_conflict: add`. **This lets anyone who can sign in
to the provider as `ada` take over the Matrix account `@ada`.** Only use it
when the provider guarantees usernames are unique and cannot be chosen freely
by the person.

## Mattermost as the provider

Mattermost is an OAuth 2.0 provider, not an OpenID Connect one: it has no
discovery document and no id token, and its token endpoint answers with
`"scope":""` and `"id_token":""`, which MAS rejects. So MAS reads the person
from Mattermost's userinfo endpoint, and the plugin offers a small token
endpoint that passes the exchange to Mattermost and returns the answer without
the empty fields.

1. In Mattermost, turn on **System Console → Integrations → OAuth 2.0 Service
   Provider** and create an OAuth application (**Integrations → OAuth 2.0
   Applications**) with the callback URL
   `https://mas.example.com/upstream/callback/<provider id>`. Mark it trusted.
2. In MAS, use this provider instead of the OpenID Connect one above:

   ```yaml
   upstream_oauth2:
     providers:
       - id: 01HQW90Z35CMXFJWQPHC3BGZGQ              # a ULID of your choice
         human_name: "Mattermost"
         client_id: "<the OAuth application's id>"
         client_secret: "<its secret>"
         token_endpoint_auth_method: client_secret_post
         discovery_mode: disabled
         authorization_endpoint: https://mattermost.example.com/oauth/authorize
         token_endpoint: https://mattermost.example.com/plugins/com.mattermost.plugin-matrix-bridge/sso/mattermost/token
         userinfo_endpoint: https://mattermost.example.com/api/v4/users/me
         fetch_userinfo: true
         scope: user
         claims_imports:
           subject:
             template: "{{ user.id }}"
           localpart:
             action: require
             template: "{{ user.username }}"
             on_conflict: fail
           displayname:
             action: suggest
             template: "{{ user.nickname | default(value=user.username) }}"
           email:
             action: suggest
             template: "{{ user.email }}"
   ```

3. The subject the bridge links is the person's **Mattermost user ID**, so
   nothing else is needed: the bridge already knows it.

`MAS Identity Provider ID` in the bridge settings is the `id` above. The
plugin's token route is public like Mattermost's own token endpoint and only
forwards to the Mattermost server it runs in.

## What is tested

The Mattermost-as-provider recipe runs in
[`e2e/mas_mattermost_test.go`](../e2e/mas_mattermost_test.go) against a real
Mattermost, MAS and Synapse: a person signs in to Matrix with their Mattermost
account and lands on the account the bridge made for them.



[`e2e/mas_test.go`](../e2e/mas_test.go) runs the whole thing against real
software: Synapse delegating to MAS, MAS with an upstream OpenID Connect
provider ([Dex](https://dexidp.io/)), and the appservice registered the way
mirror mode registers the bridge (it owns the whole user namespace). It checks
that:

- the bridge can register `@ada` through its appservice while MAS delegates
  sign-in (with `inhibit_login`),
- MAS accepts that account when told about it with `skip_homeserver_check`, and
  the account can be linked to the person's subject at the provider,
- signing in to Matrix through the provider then lands on **that** account, the
  token MAS issues is accepted by Synapse, and the person's own client and the
  bridge (through the appservice) both post as it,
- **without** the bridge telling MAS, the first sign-in fails with *Localpart
  "bob" is not available on this homeserver*, which is why the plugin does it,
- a subject that already belongs to someone else cannot be linked to another
  account.

The plugin's side (account creation, the MAS requests, retry, claiming) has
unit tests against a fake MAS. What is **not** covered by an automated test: the
plugin driving a real Mattermost signed in with OpenID Connect (the test uses
the subject the provider releases, as Mattermost records it, rather than a
Mattermost login), and identity providers other than Dex. Dex's local password
connector releases the person's name as `name` and leaves `preferred_username`
empty, so the test imports the localpart from `name`; most providers release
`preferred_username`, which is what the example above uses.

The test stack uses `discovery_mode: insecure` because Dex runs over plain
http. MAS requires an `https` issuer otherwise, and production providers
should be `https`.
