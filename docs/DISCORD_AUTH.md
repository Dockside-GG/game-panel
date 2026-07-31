# Discord authentication and invitations

Dockside uses Discord’s OAuth2 authorization-code flow with the `identify` scope. The panel stores the stable Discord user ID and display metadata. It never asks for a Discord password and does not require a bot.

## Redirect URI

The configured redirect must exactly match:

```text
<DOCKSIDE_PUBLIC_URL>/api/v1/auth/discord/callback
```

Scheme, hostname, and port are significant. `https://panel.example.com` and `https://panel.example.com:8443` are different origins.

## Owner claim

The installer generates a high-entropy bootstrap token. It can claim the first owner only once. After the owner is recorded, the token hash is cleared and cannot claim another account.

## Inviting users

Dockside deliberately does not DM Discord users:

1. The owner creates an expiring single-use invite in Users & Access.
2. The owner sends the URL through a trusted channel.
3. The recipient opens it and signs in with Discord.
4. The invite becomes consumed, but the account remains `pending`.
5. The owner reviews the Discord identity, approves it, selects a maximum panel role, and assigns server-specific roles.

A pending user cannot see the dashboard or any game server.

## MFA policy

The owner can require Discord MFA for:

- Owners and administrators.
- Administrators and operators.
- Every user.
- Nobody.

Discord reports MFA state during authentication. Changing the policy affects the next login/session validation; it does not configure MFA on Discord.

## Secret rotation

To rotate the Client Secret:

1. Generate a replacement in Discord.
2. Replace `secrets/discord_client_secret` without adding a newline or surrounding quotes.
3. Restart only the app:

   ```console
   docker compose --env-file .env up -d --no-deps --force-recreate app
   ```

Existing panel sessions remain governed by Dockside session expiration.
