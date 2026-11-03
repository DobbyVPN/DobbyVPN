# HTTPS diagnostic upload endpoint

DobbyVPN 1.5.11 supports manual log uploads when the subscription provider enables
them. Older clients reject the `logs_endpoint` setting; omit it from settings for
older or unknown versions. Automatic uploads, analysis, S3 and SFTP are outside
this feature. This guide configures a provider endpoint; it does not deploy one.

## Provider setup

Use the settings server's existing Caddy instance as an authenticated,
upload-only gateway to a dedicated Hetzner Storage Box sub-account. Enable
WebDAV and, if the settings server is outside Hetzner, External Reachability.
Give that sub-account write access to its dedicated directory. Keep the Storage
Box credentials on the server; use a different password for app uploads.

Supply these environment variables to the Caddy process through your existing
service configuration or secret management. Caddy does not automatically load
an arbitrary `.env` file.

| Variable | Value |
| --- | --- |
| `DOBBY_LOGS_PASSWORD_HASH` | Output of `caddy hash-password` for the app-facing password; the example username is `upload`. |
| `DOBBY_STORAGEBOX_HOST` | The assigned sub-account hostname, for example `u12345-sub1.your-storagebox.de`, without a scheme or path. |
| `DOBBY_STORAGEBOX_AUTH` | Base64 of the Storage Box `username:password`, without a newline. This is a secret, not a password hash. |

Replace `settings.example` with your hostname and retain your existing settings
routes in the final handler. The example returns 404 there until you add them.
Reserve `/logs` and `/logs/` exclusively for this route; do not expose the same
storage through another public handler.

```caddyfile
settings.example {
	@logs path /logs /logs/*
	handle @logs {
		basic_auth {
			upload {$DOBBY_LOGS_PASSWORD_HASH}
		}

		@upload {
			method PUT
			path_regexp archive ^/logs/([0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.gz)$
		}
		handle @upload {
			request_body {
				max_size 150MB
			}
			# Store in the sub-account root; discard query parameters.
			rewrite * /{re.archive.1}?
			reverse_proxy https://{$DOBBY_STORAGEBOX_HOST} {
				header_up Host {$DOBBY_STORAGEBOX_HOST}
				header_up Authorization "Basic {$DOBBY_STORAGEBOX_AUTH}"
				header_up If-None-Match "*"
			}
		}
		handle {
			respond "Only PUT to /logs/<archive>.gz is allowed" 405
		}
	}

	handle {
		# Replace with your existing settings-serving configuration.
		respond 404
	}
}
```

With those variables available, validate the completed configuration with
`caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile`, then reload
through your existing service procedure. Both HTTPS connections use normal
certificate verification. Public reads, listings, deletion and directory
creation are rejected; support uses separate authenticated read-only access.
Retention cleanup needs separate operator access with deletion permission.
This example does not configure cleanup jobs, request-rate limits or storage
quotas. Storage Box sub-accounts **do not provide independent storage quotas**. They
share the box's capacity; a per-request body limit does not impose a cumulative
quota. Operators must manage available capacity separately.

For DobbyVPN 1.5.11 and later, place this optional setting before any TOML
tables. Omit it for older or unrecognized clients, or to disable uploads:

```toml
logs_endpoint = "https://upload:APP_PASSWORD@settings.example/logs/"
```

Versioned 1.5.11 builds send `User-Agent: DobbyVPN/1.5.11`. Enable this setting
only for recognized compatible versions. Older builds may send `DobbyVPN/dev`,
and development builds may send `DobbyVPN/development`; omit the key for these
and any unknown version. When responses vary by User-Agent, send `Vary: User-Agent`
and configure shared caches to honor it, or use `Cache-Control: private, no-store`. An older
client must never receive a cached response containing the new key. Static Caddy
matchers can select compatible settings files; dynamic settings generators can
make the same version check.

Keep the trailing slash and percent-encode URL-reserved password characters.
The endpoint must use HTTPS and must not have a query or fragment. The app
extracts URL userinfo into Basic authentication; credentials are not part of
the request path or support reference.
The app sends an ordinary PUT containing the completed gzip archive, with
`Content-Type: application/gzip` and its actual compressed `Content-Length`.
The request includes `If-None-Match: *`; the proxy forces it upstream even if a
client omits it. Only `201 Created` confirms success. Redirects, 412, interrupted
connections and lost responses are failures or unconfirmed uploads. There is no
separate signing request, receipt response or automatic retry. The transfer is
cancellable and has a 15-minute overall deadline. A new manual attempt uses a
new complete archive and reference.

## Why the upload limit is 150 MB

The limit applies to the **whole compressed archive**, not each input log.
The required retention budget is **at most four log files total on every
platform, including current and rotated generations**, shared by backend,
native UI, CLI and stderr diagnostics. It is not four files per component.
DobbyVPN 1.5.11 consolidates producers into two destinations, each retaining its
current and previous generation. Temporary export/upload attachments have
separate lifetimes and are not additional retained log history.

At the existing 150,000,000-byte rotation threshold, four retained files give a
nominal 600 MB raw inventory. Measurements of retained backend logs on
2026-11-03 reduced a 248.2 MB macOS sample to 37.9 MB (6.55:1), and a 5.8 MB
Linux sample to 1.0 MB (5.75:1), using gzip at its default compression level.
At the lower measured ratio, 600 MB would compress to approximately 104 MB.
`150MB` is 150,000,000 bytes, allowing approximately 46 MB of headroom. This is
an initial service cap based on observed backend compression, not a guaranteed
bound or a measurement of complete mobile exports.

The 150 MB value is a rotation trigger, **not a strict per-file maximum**.
Complete records and in-flight stderr writes may cross it, so no finite archive
maximum follows from those thresholds alone. The uploader checks the
finished archive's actual size before sending. Above 150 MB it reports that the
archive exceeds the upload limit. Complete manual export and sharing remain
available; logs are not truncated to fit. The failed upload's temporary archive
is removed when its upload interaction finishes.

Caddy enforces the body limit while streaming. Oversized or interrupted uploads
may already have sent bytes to storage. Before deployment, verify Storage Box's
partial-file handling and that `If-None-Match: *` atomically refuses an existing
filename, including concurrent PUTs. A rejected or ambiguous upload must not be
shown as successful.

## Upload action and support reference

When the provider enables uploads, double-tapping the mobile log view opens
this action panel, with a full-width Upload button above Clear and Share:

```text
[      Upload     ]
[ Clear ]  [ Share ]
```

Share keeps the existing archive-sharing action. Upload sends the archive to
the configured provider and shows progress while preventing duplicate taps.
Desktop log context menus expose the same Upload action; Linux uses
`dobby-cli logs upload`. Upload is absent when
the provider has not configured an endpoint. Accessible actions remain available.

For each new archive, the app uses a standard UUIDv7 generator. The UUID is
time-ordered and contains random bits to make collisions negligibly unlikely;
the full UUID, including its hyphens, becomes the filename:

```text
019a4a6b-7c80-7d21-9f63-248e8b4c5a90.gz
```

The filename stem is the reference: no second ID, receipt service or lookup
database. UUIDv7 includes its creation time, but no device ID or account name.
Create-only storage writes prevent an existing archive from being overwritten.
A reference alone does not permit downloading the archive.

Only after the server confirms `201 Created`, Logs shows:

```text
Logs uploaded
Reference: 019a4a6b-7c80-7d21-9f63-248e8b4c5a90

[Copy reference]  [Share]
```

Copy reference copies the exact reference. Share opens the system share menu
with exactly `DobbyVPN logs: 019a4a6b-7c80-7d21-9f63-248e8b4c5a90`, without the archive,
endpoint password or an authenticated URL. The latest successful reference and
upload time and provider association remain available in Logs after reopening
the app. Replacing configuration does not change the destination of an upload
already prepared. Upload credentials are never stored with the reference. Support looks for
that reference plus `.gz` in its private storage; a user can paste the reference
into a public issue. Failed or oversized uploads show their error and retain the
existing manual export option.
