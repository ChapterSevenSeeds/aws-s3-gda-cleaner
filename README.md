# S3 GDA Cleaner

A lightweight Docker container that detects and removes stale files from AWS S3 Glacier Deep Archive (GDA) that no longer exist on the local filesystem.

## Problem

When using rclone in **copy** mode to back up files to S3 Glacier Deep Archive, deleted or renamed local files remain in S3 indefinitely. rclone can't safely **sync** to GDA because GDA has a 180-day minimum storage duration — deleting objects early incurs pro-rated charges.

## Solution

This tool runs on a cron schedule inside a Docker container. Each cycle it:

1. **Lists** all objects in the configured S3 bucket/prefix
2. **Walks** the local filesystem (volume-mounted into the container)
3. **Identifies** objects that exist in S3 but not locally, and are older than the configured stale time
4. **Deletes** those objects — either automatically or after email-based user approval

## Quick Start

1. Create a `.env` file:

```env
AWS_ACCESS_KEY_ID=AKIA...
AWS_SECRET_ACCESS_KEY=wJal...
AWS_REGION=us-east-1
S3_BUCKET=my-backup-bucket
SMTP_HOST=smtp.gmail.com
SMTP_PORT=465
SMTP_USERNAME=you@gmail.com
SMTP_PASSWORD=app-password
SMTP_FROM=you@gmail.com
SMTP_TO=you@gmail.com
```

2. Edit `docker-compose.yml` to set your volume mount and preferences, then:

```bash
docker compose up -d
```

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `AWS_ACCESS_KEY_ID` | Yes | — | AWS access key |
| `AWS_SECRET_ACCESS_KEY` | Yes | — | AWS secret key |
| `AWS_REGION` | Yes | — | AWS region (e.g., `us-east-1`) |
| `S3_BUCKET` | Yes | — | S3 bucket name |
| `S3_PREFIX` | No | `""` | S3 key prefix (if rclone writes to a subdirectory) |
| `LOCAL_PATH` | Yes | — | Path inside the container to the mounted backup directory |
| `CRON_EXPRESSION` | Yes | — | When to run (standard cron, e.g., `0 0 * * 0` for weekly) |
| `DELETION_BEHAVIOR` | Yes | — | `prompt` (email approval required) or `auto` (delete immediately) |
| `STALE_TIME` | Yes | — | Minimum object age before it's eligible for deletion (e.g., `180d`, `4320h`) |
| `SMTP_HOST` | Yes | — | SMTP server hostname |
| `SMTP_PORT` | Yes | — | SMTP server port |
| `SMTP_USERNAME` | No | — | SMTP auth username |
| `SMTP_PASSWORD` | No | — | SMTP auth password |
| `SMTP_FROM` | Yes | — | Sender email address |
| `SMTP_TO` | Yes | — | Recipient email address |
| `HOSTNAME` | No | — | Public hostname for approval links (e.g., `gda-cleaner.example.com`) |
| `LISTEN_PORT` | No | `8080` | HTTP server port |
| `APPROVAL_TOKEN_LIFETIME` | No | `0` (∞) | How long approval tokens remain valid (e.g., `24h`, `7d`) |

## Deletion Behaviors

### `prompt` — Email Approval

1. Scan finds stale files → sends you an email listing all candidates with sizes and ages
2. Email contains an **Approve Deletion** button linking back to the container's HTTP server
3. Clicking the link triggers deletion and sends a follow-up report email
4. Approval tokens are stored in memory — restarting the container invalidates all pending approvals
5. Requires `HOSTNAME` to be set if behind a reverse proxy

### `auto` — Automatic Deletion

1. Scan finds stale files → deletes them immediately
2. Sends a summary email after deletion completes

## Architecture

```
┌──────────────────────────────────────────┐
│           Docker Container               │
│                                          │
│  ┌──────────┐      ┌──────────────────┐  │
│  │Scheduler │      │  HTTP Server     │  │
│  │ (cron)   │      │  /approve/{tok}  │  │
│  └────┬─────┘      │  /health         │  │
│       │             └────────┬─────────┘  │
│       └──────┬───────────────┘            │
│        ┌─────▼──────┐                     │
│        │   Engine   │                     │
│        └──┬──┬──┬───┘                     │
│     ┌─────┘  │  └─────┐                  │
│  ┌──▼──┐  ┌──▼──┐  ┌──▼──────┐           │
│  │Scan │  │ Del │  │Notifier │           │
│  └─────┘  └─────┘  │ (SMTP)  │           │
│                     └─────────┘           │
│  Volume: /data (read-only)               │
└──────────────────────────────────────────┘
```

### Key Design Decisions

- **Go** — single static binary, tiny image (~15MB), built-in HTTP server, excellent concurrency
- **AWS SDK v2 directly** — no rclone dependency in the container; full control over pagination and batch deletion
- **Stale time = S3 object age** — uses `LastModified` timestamp; no persistent state needed; naturally aligns with GDA's 180-day minimum
- **In-memory token store** — intentionally ephemeral; container restart invalidates pending approvals
- **`Notifier` interface** — easy to add Slack, webhook, Pushover, etc. without touching core logic

### Future Extensibility

This is designed as a v1 with clean interfaces that can grow:

- **Additional notifiers**: implement the `Notifier` interface for Slack, Discord, webhooks, etc.
- **Web GUI**: HTTP server already exists — add routes for on-demand scans, viewing pending approvals, and deletion history
- **Persistent history**: add SQLite (via `modernc.org/sqlite` for pure Go) to track scan results and deletion history
- **On-demand triggers**: add an HTTP endpoint to trigger a scan outside the cron schedule

## S3 Glacier Deep Archive Notes

- **180-day minimum storage**: objects deleted before 180 days incur pro-rated early deletion charges
- **No restore needed for deletion**: you can delete GDA objects directly via the S3 API
- **LIST operations work normally**: no restore needed to enumerate objects
- **Set `STALE_TIME` to at least `180d`** to avoid early deletion fees

## Health Check

```bash
curl http://localhost:8080/health
# {"status":"ok","pending_approvals":0}
```

## Building Locally

```bash
go build -o gda-cleaner .
```

## License

MIT
