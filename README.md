# Lappu

*A note on the wall.* (*Lappu* is Finnish for a note or a slip of paper.)

An [Obsidian](https://obsidian.md) plugin and a small server for sharing individual notes online through an anonymized link, running on **your own server**.

![A preview showing off a shared note](media/preview.png)

Based on [Just Share Please](https://github.com/Ellpeck/ObsidianJustSharePlease) by [Ellpeck](https://github.com/Ellpeck) (MIT). All credit for the original idea, plugin and viewer goes to the original author. Lappu keeps the MIT license (see [LICENSE](LICENSE)). It was previously published as *Just Share Please (Self-hosted)*.

## What is different from Just Share Please

- **Go server instead of PHP.** The server is a single static Go binary in a distroless container, with the viewer embedded. It keeps the `share.php` API, so links and existing data keep working.
- **Upload token.** Creating, updating and deleting shares requires `Authorization: Bearer <token>`. Reading shares does not.
- **Security fixes.**
  - Raw shares are served as `text/plain` with `nosniff`, so stored XSS is no longer possible.
  - A DOM XSS in the viewer is fixed.
  - The viewer sends a strict Content-Security-Policy and loads its CDN libraries with Subresource Integrity.
  - New share ids are 96-bit instead of 32-bit, and ids are validated on every request.
  - Share passwords are stored as SHA-256 hashes.
  - Requests have a size limit and timeouts.
  - Failed write attempts are rate-limited per IP. A request with the valid token is never blocked.
  - The container runs as a non-root user on a read-only root filesystem.
- **Bilingual UI.** The plugin and the viewer are available in English and Finnish. The language follows the system or browser, and you can change it in the plugin settings or with the FI / EN switch in the viewer footer.
- **Own plugin id** (`lappu`), so Obsidian never replaces it with the upstream plugin. It can import shares from the original plugin and from *Just Share Please (Self-hosted)*.

## Installing the plugin

The plugin is not in the Obsidian community plugin list. Install it in one of these ways:

- **BRAT:** install [BRAT](https://github.com/TfTHacker/obsidian42-brat), choose *Add beta plugin* and enter `mhavo/lappu`. BRAT installs the latest release and keeps it updated. The repository is private, so first add a GitHub personal access token in BRAT's settings: a fine-grained token limited to `mhavo/lappu` with read-only *Contents* access is enough.
- **Manually:** download `main.js`, `manifest.json` and `styles.css` from the [latest release](https://github.com/mhavo/lappu/releases/latest). Put them in `<vault>/.obsidian/plugins/lappu/`, then enable *Lappu* under *Settings → Community plugins*.

Then open the plugin settings and set:

- **Server URL:** the address of your server, for example `https://lappu.example.com`.
- **Token:** the same value as `LAPPU_UPLOAD_TOKEN` on the server.

To share a note, use *Share as Lappu* in the file menu or *Share current file as Lappu* in the command palette. The link is copied to the clipboard. The *Lappu* view lists your shares and lets you copy, update or delete them.

## Self-hosting

The server lives in [`server/`](server); the binary is built from `server/cmd/lappu` and the compose file builds an image named `lappu`. The included [`server/docker-compose.yml`](server/docker-compose.yml) is a starting point:

```sh
cd server
export LAPPU_UPLOAD_TOKEN="$(openssl rand -hex 32)"   # keep this, the plugin needs it
mkdir -p data quotes && sudo chown -R 33:33 data quotes
docker compose up -d --build
```

The server listens on `127.0.0.1:8080`. Put a reverse proxy with TLS (for example Caddy) in front of it. The compose file runs the container with a read-only root filesystem, all capabilities dropped and `no-new-privileges`.

- **User:** the image runs as UID/GID **33** (`www-data`), the same owner as the data directory of the old PHP image. Either give `data/` and `quotes/` to 33:33, or set `user:` in compose to the owner of your volumes.
- **Health check:** `GET /healthz` returns `200 ok`, and `GET /` serves the viewer. The image has no shell, so put health checks on the host or the proxy, not inside the container.
- **Quotes:** the viewer footer shows a random quote from `quotes/quotes.csv` (`"Quote";"Source"` per line). The file is re-read on every request. See [`server/quotes/README.md`](server/quotes/README.md).

### Configuration

| Variable | Default | Description |
|---|---|---|
| `LAPPU_LISTEN` | `:80` | Listen address. Binding `:80` as non-root works because Docker allows unprivileged ports in containers; use e.g. `:8080` otherwise. |
| `LAPPU_DATA_DIR` | `/var/www/data` | Directory for shares (`<id>.md` and `<id>.json`). Created with mode `0700` if missing. |
| `LAPPU_QUOTES_FILE` | `/var/www/quotes/quotes.csv` | Quotes file for the viewer footer. Optional. |
| `LAPPU_UPLOAD_TOKEN` | – | Upload token, at least 32 characters. Required unless anonymous uploads are allowed. |
| `LAPPU_UPLOAD_TOKEN_FILE` | – | Read the token from a file instead (Docker secrets etc.). Set only one of `LAPPU_UPLOAD_TOKEN` and `LAPPU_UPLOAD_TOKEN_FILE`. |
| `LAPPU_ALLOW_ANONYMOUS_UPLOADS` | `false` | `true` lets anyone write without a token, e.g. for the unmodified upstream plugin. Not recommended. |
| `LAPPU_MAX_BODY_BYTES` | `20971520` (20 MiB) | Maximum request body size. Images are embedded as base64, so the limit is generous. |
| `LAPPU_TRUSTED_PROXIES` | `127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7` | Comma-separated CIDRs or IPs whose `X-Forwarded-For` header is trusted for the client IP (rate limiting and logs). |

The server refuses to start if no token is set and `LAPPU_ALLOW_ANONYMOUS_UPLOADS` is not `true`, if the token is shorter than 32 characters, or if any old `JSP_*` variable (the pre-rename names) is still set.

If you deploy behind a reverse proxy on the same host, the proxy usually reaches the container through the published port, so the container only sees the Docker gateway address. The default `LAPPU_TRUSTED_PROXIES` covers private and Docker networks for that reason. Narrow it if untrusted clients can reach the port directly.

### API

All calls go to `/share.php`, as with the PHP server. Write calls need `Authorization: Bearer <token>`.

| Request | Result |
|---|---|
| `GET ?id=<id>` | Markdown as `text/plain`, or 404 |
| `GET ?meta&id=<id>` | JSON `{"id","created"}` |
| `GET ?quote` | JSON `{"quote","source"}` |
| `POST`, body `{"content": "..."}` | JSON `{"id","password","created"}`; the password is returned only here |
| `PATCH ?id=<id>`, header `Password`, body `{"content"}` | 200, or 400 / 401 / 404 |
| `DELETE ?id=<id>`, header `Password` | 200, or 400 / 401 / 404 |

A missing or wrong token returns `401 Invalid token`, and a wrong share password returns `401 Unauthorized`. Too many failed attempts return 429, and an oversized body returns 413.

## Migrating from the PHP server

1. **The data directory works as-is.** Mount the existing `data/` volume at `/var/www/data`. The files keep the same names, and old 8-character ids and links keep working.
2. **The owner is UID 33**, as in the PHP image, so no `chown` is needed.
3. **Set the token** (`LAPPU_UPLOAD_TOKEN` or `LAPPU_UPLOAD_TOKEN_FILE`) and enter the same value in the plugin settings.
4. **Legacy shares migrate lazily.** Old metadata with a plaintext password stays readable. The first successful update with the share's password rewrites the metadata, replacing the password with its hash. You don't need to run a migration step.

If you deploy with Ansible, the only changes needed are a `LAPPU_UPLOAD_TOKEN` (or `LAPPU_UPLOAD_TOKEN_FILE`) environment variable. The build context (`server/`), port 80 in the container, volume paths and the `GET /` health check stay the same.

## Migrating from Just Share Please (Self-hosted)

Lappu is the same plugin under a new id, so Obsidian treats it as a separate plugin. To move over:

1. Install Lappu with BRAT (`mhavo/lappu`) and enable it. Keep the old plugin installed for now.
2. Lappu offers to import the old plugin's data on first start. Otherwise use the *Import shares from Just Share Please* button in the Lappu settings. Your shares, server URL, token and language come along.
3. Check that the Lappu view lists your shares, then disable and remove *Just Share Please (Self-hosted)*.

If you run the server, rename its environment variables from `JSP_*` to `LAPPU_*` (same suffixes). The server refuses to start while any of the old `JSP_*` variables is still set, and names each one it found.

## Importing from earlier plugins

If *Just Share Please (Self-hosted)* or the original *Just Share Please* plugin is installed in the same vault, Lappu offers to import their data on first start. You can also import later with the command *Import shares from Just Share Please* or the button in the plugin settings. The self-hosted version is merged first, so its server URL wins.

The import works as follows:

- It copies the share list (id, password and note path) and the options.
- It skips shares that already exist.
- It takes the server URL only if none is set yet, and never takes the public `jsp.ellpeck.de` URL.
- From *Just Share Please (Self-hosted)* it also takes the token if none is set yet, and the language if it is still *Automatic*. The token and language are never taken from the original plugin.

Imported shares can be updated and deleted only if they live on your server, for example after migrating a self-hosted PHP instance's data directory.

## Roadmap

- **Phase 2: expiring shares.** Choose an expiry when sharing (never, 1 day, 1 week or 30 days). Expired shares return 404 and are cleaned up in the background.
- **Phase 3: encrypted shares.** The plugin encrypts the note with a read password (AES-256-GCM with Argon2id, SimpleTextCrypt-compatible), and the viewer decrypts it in the browser. The server never sees the plaintext or the password.
- **Phase 4: attachments as files.** Local images are uploaded next to the share instead of being embedded as base64. This also fixes local images that don't show up today.

## Development

```sh
npm ci && npx vitest run && npm run build             # plugin
node --test "server/viewer-tests/*.test.mjs"                       # viewer
cd server && go vet ./... && go test -race ./...       # server
```

CI runs all of these and builds the Docker image. Pushing a `x.y.z` tag creates a GitHub release with `main.js`, `manifest.json` and `styles.css`.
