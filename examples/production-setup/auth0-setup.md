# Auth0 OpenID Connect (OIDC) Setup for Mattermost

## Step 1: Create Auth0 Application

1. Go to [Auth0 Dashboard](https://manage.auth0.com/)
2. Click **Applications** → **Create Application**
3. Name: `Mattermost`
4. Type: **Regular Web Application**
5. Click **Create**

## Step 2: Configure Application Settings

In the application settings:

### Application URIs
- **Allowed Callback URLs**:
  ```
  https://YOUR_DOMAIN.com/signup/openid/complete
  ```
- **Allowed Logout URLs**:
  ```
  https://YOUR_DOMAIN.com
  ```
- **Allowed Web Origins**:
  ```
  https://YOUR_DOMAIN.com
  ```

### Advanced Settings → OAuth
- **JsonWebToken Signature Algorithm**: RS256
- **OIDC Conformant**: Enabled

Click **Save Changes**

## Step 3: Get Credentials

Copy from the application settings:
- **Client ID**: `YOUR_CLIENT_ID`
- **Client Secret**: `YOUR_CLIENT_SECRET`
- **Domain**: `YOUR_DOMAIN.auth0.com`

## Step 4: Configure Mattermost

### Via System Console

1. Go to **System Console** → **Authentication** → **OpenID Connect**
2. Configure:
   - **Enable OpenID Connect**: true
   - **Select Provider**: Other
   - **Discovery Endpoint**: `https://YOUR_DOMAIN.auth0.com/.well-known/openid-configuration`
   - **Client ID**: (Your Auth0 Client ID)
   - **Client Secret**: (Your Auth0 Client Secret)
   - **Button Name**: Auth0
   - **Button Color**: #EB5424

### Via Environment Variables

Add to `docker-compose.yml`:

```yaml
environment:
  - MM_OPENIDSETTINGS_ENABLE=true
  - MM_OPENIDSETTINGS_BUTTONNAME=Auth0
  - MM_OPENIDSETTINGS_BUTTONCOLOR=#EB5424
  - MM_OPENIDSETTINGS_DISCOVERYENDPOINT=https://YOUR_DOMAIN.auth0.com/.well-known/openid-configuration
  - MM_OPENIDSETTINGS_ID=YOUR_CLIENT_ID
  - MM_OPENIDSETTINGS_SECRET=YOUR_CLIENT_SECRET
```

Restart Mattermost:
```bash
docker-compose restart mattermost
```

## Step 5: Handle Existing Users

If you have existing email/password users who want to use Auth0:

### Option 1: Users Link Accounts Manually (Recommended)
1. User signs in with email/password
2. Go to **Settings** → **Security** → **Sign-in Method**
3. Click "Switch to Auth0"
4. Redirected to Auth0 to confirm

### Option 2: Delete and Recreate via Auth0
For test environments, you can delete users and have them sign up fresh via Auth0:

```bash
# Delete a user
ssh root@YOUR_SERVER "docker exec mattermost mmctl --local user delete user@example.com --confirm"

# User then signs in via Auth0, which auto-creates their account
```

## Troubleshooting

### "Bad response from token request" (401 Unauthorized)

**Causes**:
1. Client Secret mismatch - Copy secret again from Auth0
2. Application Type is "Single Page Application" - Must be "Regular Web Application"
3. Callback URL doesn't match exactly

**Check in Auth0**:
- Application Type: Regular Web Application
- Token Endpoint Authentication Method: POST (in Advanced Settings → OAuth)
- Application is enabled

### "OAuth Error: invalid_request"
- Verify the **Allowed Callback URL** in Auth0 matches exactly: `https://YOUR_DOMAIN.com/signup/openid/complete`
- Check for trailing slashes, http vs https

### "Account already exists with different sign-in method"
- User has existing email/password account
- See "Handle Existing Users" section above

### Automatic Account Creation
If users cannot sign up, ensure automatic account creation is enabled:
- **System Console** → **Authentication** → **OpenID Connect** → **Enable Automatic Account Creation**: true

## Security Best Practices

1. **Use Strong Client Secrets**: Auth0 generates these automatically
2. **Enable MFA**: Configure in Auth0 for added security  
3. **Restrict Domains**: In Auth0, limit sign-ups to specific email domains
4. **Regular Audits**: Review user access and permissions periodically

## Migration to Matrix Authentication Service

Once Matrix is set up with mirror mode, consider migrating to Matrix Authentication Service (MAS):
- Single sign-on across Mattermost and Matrix
- Unified user management
- See: https://github.com/matrix-org/matrix-authentication-service
