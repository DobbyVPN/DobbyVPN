# HTTPS diagnostic upload endpoint

**Preview: DobbyVPN does not implement log uploads yet.** This page describes
the proposed provider configuration and support reference. Current releases
reject the `logs_endpoint` setting; do not add it to their subscriptions.
Automatic uploads on reconnection/failure and automatic analysis are deferred.
The initial transport is WebDAV over HTTPS; SFTP and S3 are deferred.

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
			path_regexp archive ^/logs/([A-Za-z0-9_-]{16,96}\.gz)$
		}
		handle @upload {
			request_body {
				max_size 500MB
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
quotas. Storage Box sub-accounts share the box's capacity without individual
storage quotas.

For a future supporting DobbyVPN release, place this optional setting before
any TOML tables. Omit it for older or unrecognized clients, or to disable uploads:

```toml
logs_endpoint = "https://upload:APP_PASSWORD@settings.example/logs/"
```

Keep the trailing slash and percent-encode URL-reserved password characters.
The app sends an ordinary PUT containing the completed gzip archive, with
`Content-Type: application/gzip` and its actual compressed `Content-Length`.
There is no separate signing request or custom receipt response.

## Why the upload limit is 500 MB

The limit applies to the **whole compressed archive**, not each input log.
Each log rotates at 150,000,000 bytes and retains its current and previous
generations. Files are created as their streams are used; a previous generation
appears after rotation. The counts below are possible retained log files, not
files preallocated on every device. The current inventories give these nominal
raw sizes when every generation reaches the rotation threshold:

| Export | Log streams | Maximum log files | Nominal total |
| --- | ---: | ---: | ---: |
| macOS / Windows app | 3 | 6 | 900 MB |
| Android app | 4 | 8 | 1,200 MB |
| iOS app / macOS CLI | 7 | 14 | 2,100 MB |
| Linux CLI | 6 | 12 | 1,800 MB |
| Windows CLI | 5 | 10 | 1,500 MB |

`500MB` is 500,000,000 bytes of compressed data. Measurements of retained backend
logs on 2026-11-03 reduced a 248.2 MB macOS sample to 37.9 MB (6.55:1), and a
5.8 MB Linux sample to 1.0 MB (5.75:1), using gzip at its default compression
level. At the lower measured ratio, the largest nominal 2.1 GB inventory would
be about 365 MB, leaving approximately 135 MB of headroom. This is a practical
initial service cap based on observed compression, not a guaranteed bound or a
measurement of complete mobile exports. Missing files reduce actual size.

The 150 MB value is a rotation trigger, **not a strict per-file maximum**.
Complete records and in-flight stderr writes may cross it, so no finite archive
maximum follows from those thresholds alone. The proposed uploader checks the
finished archive's actual size before sending. Above 500 MB it reports that the
archive exceeds the upload limit and preserves the complete local export for
manual sharing; it does not truncate logs to fit.

Caddy enforces the body limit while streaming. Oversized or interrupted uploads
may already have sent bytes to storage. Before deployment, verify Storage Box's
partial-file handling and that `If-None-Match: *` atomically refuses an existing
filename, including concurrent PUTs. A rejected or ambiguous upload must not be
shown as successful.

## Planned Upload action and support reference

When the provider enables uploads, double-tapping the mobile log view opens
this action panel, with a full-width Upload button below Clear and Share:

```text
[ Clear ]  [ Share ]
[      Upload     ]
```

Share keeps the existing archive-sharing action. Upload sends the archive to
the configured provider and shows progress while preventing duplicate taps.
Desktop log context menus expose the same Upload action. Upload is absent when
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

Only after the server confirms `201 Created`, the planned Logs screen shows:

```text
Logs uploaded
Reference: 019a4a6b-7c80-7d21-9f63-248e8b4c5a90

[Copy reference]  [Share]
```

Copy reference copies the exact reference. Share opens the system share menu
with text such as `DobbyVPN logs: 019a4a6b-7c80-7d21-9f63-248e8b4c5a90`, without the archive,
endpoint password or an authenticated URL. The latest successful reference and
upload time remain available in Logs after reopening the app. Support looks for
that reference plus `.gz` in its private storage; a user can paste the reference
into a public issue. Failed or oversized uploads show their error and retain the
existing manual export option.
