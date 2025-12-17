# DocuSign Connector Setup Guide

---

## Connector capabilities

1. **What resources does the connector sync?**  
   This connector syncs:  
   — Users  
   — Groups  
   — Signing Groups
   — Permissions Profile

2. **Can the connector provision any resources? If so, which ones?**

   The connector supports the following provisioning operations:

   **Account Provisioning:**

   - Users (create new user accounts)

   **Grant (Add Access):**

   - Group membership (add users to groups)
   - Signing group membership (add users to signing groups)
   - Permission profile assignment (assign permission profiles to users)

   **Revoke (Remove Access):**

   - Group membership (remove users from groups)
   - Signing group membership (remove users from signing groups)
   - Permission profile revoke (assigns "DocuSign Viewer" - the default read-only profile)

   **Important Note about Permission Profiles:**

   - DocuSign requires all users to have at least one permission profile assigned at all times
   - When revoking a permission profile, the connector automatically assigns "DocuSign Viewer" (the minimum permission level)
   - **Cannot revoke "DocuSign Viewer"**: If a user already has the "DocuSign Viewer" profile, the revoke operation will fail with an error, as there is no lower permission level available
   - Built-in profiles ("DocuSign Admin", "DocuSign Sender", "DocuSign Viewer") cannot be edited or deleted per DocuSign's design

---

## Connector credentials

1. **What credentials or information are needed to set up the connector?**  
   This connector requires:  
   — Client ID (Integration Key)  
   — Client Secret  
   — Redirect URI  
   — Refresh Token  
   — Demo mode flag (optional, defaults to demo environment)

   **Command-line Arguments**:

   ```
   --clientId          OAuth 2.0 Client ID (Integration Key)
   --clientSecret      OAuth 2.0 Client Secret
   --redirect-uri      Redirect URI registered in your integration
   --refresh-token     OAuth 2.0 Refresh Token
   --demo             Set to true for demo, false for production (default: true)
   --include-signing-groups  Enable syncing of signing groups (optional)
   ```

2. **For each item in the list above:**

   - **How does a user create or look up that credential or info?**

     **Step 1: Create Integration in DocuSign Admin**

     1. Log in to [DocuSign Admin](https://apps-d.docusign.com/admin/apps-and-keys) (demo) or [DocuSign Admin](https://apps.docusign.com/admin/apps-and-keys) (production)
     2. Click **Add App and Integration Key**
     3. Enter a name for your app (e.g., "Baton Connector")
     4. Click **Create App**

     **Step 2: Configure OAuth Settings**

     5. In the app configuration:
        - **Client ID**: Copy the Integration Key (automatically generated)
        - **Client Secret**: Click **Add Secret Key**, copy and save it securely
        - **Redirect URI**: Add your redirect URI (e.g., `http://example.com/callback`)
        - Enable **Authorization Code Grant** under Authentication
        - Under **CORS Settings** enable GET, POST, PUT, DELETE, and HEAD
     6. Click **Save**

     **Step 3: Obtain Refresh Token Using the Connector**

     The connector provides a convenient `--configure` flag to obtain your refresh token automatically:

     ```bash
     baton-docusign \
       --demo=true \
       --clientId "YOUR_CLIENT_ID" \
       --clientSecret "YOUR_CLIENT_SECRET" \
       --redirect-uri "http://example.com/callback" \
       --configure
     ```

     This interactive command will:

     1. Display an authorization URL for you to visit
     2. Prompt you to authorize the application in your browser
     3. Ask you to paste the authorization code from the redirect URL
     4. Automatically exchange the code for a refresh token
     5. Display the refresh token for you to save

     **Example flow:**

     ```
     Please visit the following URL to authorize the application:

     https://account-d.docusign.com/oauth/auth?response_type=code&scope=signature&client_id=...

     Enter the authorization code: <paste code here>

     refresh token: eyJ0eXAiOiJNVCIsImFsZyI6...
     ```

     After visiting the authorization URL and granting access, you'll be redirected to:

     ```
     http://example.com/callback?code=AUTHORIZATION_CODE
     ```

     Copy the `code` parameter value and paste it when the connector prompts you. Save the displayed refresh token securely for future use.

     **Alternative: Manual OAuth Flow**

     If you prefer to obtain the refresh token manually, follow the [DocuSign OAuth Guide](https://developers.docusign.com/platform/auth/authcode/authcode-get-token/)

   - **Does the credential need any specific scopes or permissions?**  
     Yes. Your app must be authorized to use OAuth2 Authorization Code Grant and have access to read user and group data, as well as manage users (for provisioning).

   - **Is the list of scopes or permissions different to sync (read) versus provision (read-write)?**  
     Yes.

     - **Syncing (read-only)**: Requires access to read users, groups, and permissions.
     - **Provisioning (read-write)**: Requires permission to create users in your DocuSign account.

   - **What level of access or permissions does the user need in order to create the credentials?**  
     The user must have access to the **Admin Console** in DocuSign to
     create and configure apps and keys.

---

## Additional Configuration

### Signing Groups

DocuSign Signing Groups are an optional feature. To sync signing groups:

1. Ensure your DocuSign account has the Signing Groups feature enabled.
2. Add the `--include-signing-groups` flag when running the connector.
3. The connector will sync signing group memberships along with regular groups.

### Environment Selection

- **Demo Environment** (default): Use `--demo=true` or omit the flag
  - Base URL: `https://demo.docusign.net`
  - OAuth URL: `https://account-d.docusign.com`
- **Production Environment**: Use `--demo=false`
  - Base URL: `https://na3.docusign.net` (or your account's base URL)
  - OAuth URL: `https://account.docusign.com`

### Refresh Token Best Practices

1. **Security**: Store refresh tokens securely. They provide long-term access to your DocuSign account.
2. **Expiration**: DocuSign refresh tokens do not expire unless explicitly revoked, but it's good practice to rotate them periodically.
3. **Environment-specific**: Refresh tokens obtained in the demo environment cannot be used in production and vice versa.
4. **Revocation**: You can revoke refresh tokens at any time from the DocuSign Admin Console under Apps and Keys.
5. **Regeneration**: If you lose your refresh token or it's compromised, use the `--configure` flag to generate a new one.

### Troubleshooting

**Issue: "oauth2: token expired and refresh token is not set"**

- This error occurs during the `--configure` step if there's an authentication issue
- Solution: The connector has been updated to handle this correctly. Make sure you're using the latest version.

**Issue: Authorization code already used**

- Authorization codes are single-use only
- Solution: If you make a mistake, visit the authorization URL again to get a new code

**Issue: Invalid redirect URI**

- The redirect URI in your command must exactly match the one registered in DocuSign
- Solution: Check your DocuSign app configuration and ensure the redirect URI matches exactly (including protocol, domain, and path)

---
