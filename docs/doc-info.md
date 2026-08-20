# DocuSign Connector Setup Guide

---

## Connector capabilities

1. **What resources does the connector sync?**  
   This connector syncs:  
   — Users  
   — Groups  
   — Signing Groups
   — Permissions Profile
   — CLM Members, Roles, Groups, Folders, Folder Security, and Permission Sets (accounts with a DocuSign CLM subscription)

2. **Can the connector provision any resources? If so, which ones?**

   The connector supports the following provisioning operations:

   **Account Provisioning:**

   - Users (create new user accounts)

   **Grant (Add Access):**

   - Group membership (add users to groups)
   - Signing group membership (add users to signing groups)
   - Permission profile assignment (assign permission profiles to users)
   - CLM group membership (requires a CLM subscription)
   - CLM folder security (requires a CLM subscription)

   **Revoke (Remove Access):**

   - Group membership (remove users from groups)
   - Signing group membership (remove users from signing groups)
   - Permission profile revoke (assigns "DocuSign Viewer" - the default read-only profile)
   - CLM group membership (requires a CLM subscription)
   - CLM folder security (requires a CLM subscription; sets the entry's access to "No Access")

   **Important Note about Permission Profiles:**

   - DocuSign requires all users to have at least one permission profile assigned at all times
   - When revoking a permission profile, the connector automatically assigns "DocuSign Viewer" (the minimum permission level)
   - **Cannot revoke "DocuSign Viewer"**: If a user already has the "DocuSign Viewer" profile, the revoke operation will fail with an error, as there is no lower permission level available
   - Built-in profiles ("DocuSign Admin", "DocuSign Sender", "DocuSign Viewer") cannot be edited or deleted per DocuSign's design

   **Important Note about CLM:**

   - CLM (Contract Lifecycle Management) is a separate, separately-licensed DocuSign product with its own API. The 5 CLM resource types carry `OptInRequired` and don't sync until a customer explicitly enables them in C1's sync configuration; C1's opt-in toggle doesn't validate the underlying subscription/scopes first, so an account that opts in but can't reach CLM fails the sync loudly rather than silently syncing zero CLM resources.
   - Requires a DocuSign CLM production subscription.
   - When using ConductorOne's managed OAuth app (the default cloud-hosted authentication method), CLM also requires that managed app to be granted the CLM API scope on ConductorOne's platform side — this is outside the connector's own configuration. Self-hosted or demo-environment setups using a customer-supplied DocuSign app do not have this extra requirement.
   - CLM permission sets sync for visibility only; DocuSign's CLM API has no endpoint to assign or unassign one, so they cannot be granted or revoked.
   - CLM members are synced as their own resource type rather than merged into the existing eSignature "Users" resource, since the two could not be confirmed to represent the same identity.

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

### CLM (Contract Lifecycle Management)

DocuSign CLM is a separate, separately-licensed DocuSign product. To sync CLM data:

1. Confirm your DocuSign account has a CLM production subscription.
2. Confirm the credential has been granted the CLM OAuth scopes (`impersonation`/`spring_read`/`spring_write`).
3. The connector then syncs CLM Members, Roles, Groups, Folders, Folder Security, and Permission Sets once a customer explicitly enables each CLM resource type in C1's sync configuration (see the CLM note above — these types carry `OptInRequired`).
4. An already-connected credential keeps its old consent on refresh (refresh tokens don't resend scopes) — re-run with `--configure` once to re-consent and pick up the new `impersonation` scope.

If running against ConductorOne's managed OAuth app (the default cloud-hosted
production authentication method), the managed app also needs the CLM API scopes
granted on ConductorOne's platform side before any CLM data syncs — this is not
something the connector's own configuration controls.

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
