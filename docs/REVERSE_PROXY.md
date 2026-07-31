# Reverse proxy and shared-host configuration

Use existing-proxy mode when the host already serves Nginx, Caddy, Apache, IIS, or other websites. The installer binds Dockside Caddy to one loopback address and port, typically `127.0.0.1:8080`. It does not edit or restart the host proxy.

## Nginx

The installer writes `deploy/generated/nginx-dockside.conf` with the exact hostname and loopback port. Add certificate directives and include that one vhost.

Required behavior:

- Proxy HTTP/1.1 to `http://127.0.0.1:<port>`.
- Preserve `Host`.
- Set `X-Forwarded-Proto`, `X-Forwarded-For`, and `X-Real-IP`.
- Forward `Upgrade` and `Connection` for live console streams.
- Use long read/write timeouts.
- Route only the selected `server_name`.

Validate before reload:

```console
nginx -t
```

Then reload using the method appropriate to your operating system. Existing vhosts remain independent because the Dockside config has a dedicated hostname.

## Caddy

An existing host Caddy can use:

```caddyfile
panel.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Keep the Dockside `.env` value:

```dotenv
DOCKSIDE_PUBLIC_URL=https://panel.example.com
DOCKSIDE_BIND_ADDRESS=127.0.0.1
DOCKSIDE_HTTP_PORT=8080
DOCKSIDE_CADDYFILE=./deploy/caddy/Caddyfile
DOCKSIDE_SECURE_COOKIES=true
```

## Apache

Enable proxy, proxy_http, headers, and websocket support, then proxy the dedicated virtual host to the same loopback upstream. Preserve the original host and forwarded HTTPS scheme.

## Origin and callback checks

Dockside uses the exact `DOCKSIDE_PUBLIC_URL` for:

- Discord callback generation.
- CSRF Origin checks on state-changing requests.
- Secure cookie policy.

If the user-visible URL changes, update the Discord redirect and `.env`, then recreate the app and gateway. Do not publish the app or engine container ports directly.
