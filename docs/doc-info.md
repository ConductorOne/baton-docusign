# DocuSign Connector Setup Guide

While developing the connector, please fill out this form. This information is needed to write docs and to help other users set up the connector.

---

## Connector capabilities

1. **What resources does the connector sync?**  
   This connector syncs:  
   — Users  
   — Groups  
   — Permissions

2. **Can the connector provision any resources? If so, which ones?**  
   The connector can provision:  
   — Users (Account provisioning)

---

## Connector credentials

1. **What credentials or information are needed to set up the connector?**  
   This connector requires:  
   — Account ID  
   — Client ID  
   — Client Secret  
   — Redirect URI

   **Args**:  
   `--account-id`  
   `--clientId`  
   `--clientSecret`  
   `--redirect-uri`

2. **For each item in the list above:**

   - **How does a user create or look up that credential or info?**

     1. Log in to [DocuSign Admin](https://apps-d.docusign.com/admin/apps-and-keys).
     2. Click on **"Add App and Integration Key"**.
     3. Choose a name for your app and click **"Create App"**.
     4. In the app configuration screen:
        - **Client ID (Integration Key)**: Automatically generated.
        - **Client Secret**: Click **"Add Secret Key"**, then copy and save it.
        - **Redirect URI**: Enter any URI of your choice (e.g., `http://example.com/callback`) and click **Add**.
        - **Account ID**: Found in the same section under "API Account ID".
     5. Click **Save** at the bottom of the page.

     > **Note**: All required credentials are accessible in the [Apps and Keys](https://apps-d.docusign.com/admin/apps-and-keys) section of your DocuSign account.

   - **Does the credential need any specific scopes or permissions?**  
     Yes. Your app must be authorized to use OAuth2 Authorization Code Grant and have access to read user and group data, as well as manage users (for provisioning).

   - **Is the list of scopes or permissions different to sync (read) versus provision (read-write)?**  
     Yes.

     - **Syncing (read-only)**: Requires access to read users, groups, and permissions.
     - **Provisioning (read-write)**: Requires permission to create users in your DocuSign account.

   - **What level of access or permissions does the user need in order to create the credentials?**  
     The user must have access to the **Admin Console** in DocuSign to create and configure apps and keys.

---
