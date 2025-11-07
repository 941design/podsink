# Podsink — Production-Ready CLI Podcast Downloader

## Purpose & Scope
**Podsink** is a production-ready command-line tool for discovering, subscribing to, and downloading podcast episodes.  
It offers a REPL-style interactive interface similar to *psql* or *inquirer.js*, with scrollable lists for search results and episodes.  
It supports persistence of subscriptions and episodes via SQLite, configuration in the user’s home directory, and explicit on-demand downloading to an external storage location (e.g., SD card).

### Primary Users
- Technically inclined podcast listeners preferring a terminal interface.
- Users managing podcasts on systems with removable or external storage.

### Desired Outcomes
- Efficient discovery, subscription, and downloading of podcasts via CLI.
- Full control over file storage and download management.
- Persisted state without automatic sync to filesystem.

### Non-Goals
- Full-screen TUI dashboard.
- Automatic syncing or caching.
- Cloud integration or telemetry.

### Target Release Type
Production-ready v1.0.

---

## Core Functionality

### Features
1. **Search** podcasts using the Apple iTunes Search API.
2. **Subscribe / Unsubscribe** to podcasts (persisted in SQLite).
3. **List Subscriptions** (`list subscriptions`) with counts of new/unplayed episodes.
4. **View Episodes** (`episodes [podcast]`) with scrollable/autocomplete UI.
5. **Queue / Download** episodes on-demand with resumable transfers.
6. **Ignore / Unignore** episodes manually.
7. **Manage Config** interactively (`config` command).
8. **Onboarding** on first run (prompt for target directory).
9. **Concurrent Downloads** with configurable concurrency and retry logic.

### REPL Interface
- Prompt: `podsink>`
- Autocomplete and inline help.
- Commands:
  - `search [query]`
  - `list subscriptions`
  - `episodes [podcast]`
  - `queue [episode_id]`
  - `download [episode_id]`
  - `ignore [episode_id]`
  - `config`
  - `exit` / `quit`

### Episode State Machine
| State | Description | Transitions |
|--------|--------------|--------------|
| `NEW` | Newly discovered episode | → `SEEN` |
| `SEEN` | Visible in UI, no user action yet | ↔ `IGNORED`, → `QUEUED` |
| `IGNORED` | User suppressed | ↔ `SEEN` |
| `QUEUED` | Selected for download | → `DOWNLOADED` |
| `DOWNLOADED` | Successfully downloaded | → `QUEUED` (re-download) |

Failures are logged but do not alter persistent state.

---

## Non-Functional Requirements

### Performance
- Startup < 1s.
- UI response latency < 50ms.
- Throughput limited only by network/disk.

### Reliability
- Atomic database writes.
- Recover from network errors without data loss.

### Security & Privacy
- HTTPS-only; strict TLS verification.
- No telemetry, analytics, or background calls.
- 0600 permissions for user files.

### Accessibility
- Keyboard navigation; visible focus cues.

### Observability
- Logs written to `~/.podsink/podsink.log`.
- Rotation: 10 MB × 3 files.
- Levels: INFO, WARN, ERROR.

### Scalability
- Handles ~500 subscriptions and ~5,000 episodes efficiently.

---

## Data & Interfaces

### Storage
- **Config:** `~/.podsink/config.yaml`
- **Database:** `~/.podsink/app.db` (SQLite)
- **Logs:** `~/.podsink/podsink.log`
- **OPML import/export:** `~/.podsink/subscriptions.opml`

### Command-line Options
- `--import-opml <path>` imports subscriptions from an OPML file and exits before starting the REPL.
- `--export-opml <path>` exports current subscriptions to an OPML file and exits before starting the REPL.

### Config Keys
| Key | Default | Description |
|------|----------|-------------|
| `download_root` | user-selected | External storage root (prompted at first run) |
| `parallel_downloads` | 4 | Max concurrent downloads |
| `tmp_dir` | `/tmp` | Temporary download directory |
| `retry_count` | 3 | Max retries |
| `retry_backoff` | exponential, max 60s | Retry backoff policy |
| `user_agent` | `podsink/<version>` | Custom user agent |
| `proxy` | optional | HTTP proxy URL |
| `tls_verify` | true | TLS strictness |

### Data Model Highlights
**Podcast:** `id`, `title`, `feed_url`, `subscribed_at`  
**Episode:** `id`, `podcast_id`, `title`, `state`, `downloaded_at`, `file_path`, `hash`, `retry_count`  
**Download Queue:** in-memory with persistent metadata.

---

## UX / Behavior

### First Run
1. Check for config file; if missing, prompt user for download directory (autocomplete paths).
2. Save YAML config and initialize SQLite DB.

### REPL Flow
- All commands executed interactively in REPL (no full-screen TUI).
- Scrollable list views for results and episodes within REPL context.
- Keyboard shortcuts: ↑↓ navigation, space toggle, Enter select, `d` download, `i` ignore.

### Search & Subscribe Flow
After running `search <query>` or `list subscriptions`, an interactive list of podcasts is displayed:

**List View:**
- Navigate with ↑↓ or j/k keys
- Press `Enter` to view podcast details
- Press `s` to subscribe directly (stays in list view)
- Press `u` to unsubscribe directly (stays in list view)
- Press `Esc` or `q` to exit search mode
- Subscribed podcasts are shown in green with a `[subscribed]` suffix
- The `list subscriptions` view shares the same layout, showing only subscribed podcasts with episode counts in the subtitle

**Details View:**
- Displays full podcast information including description
- Press `s` to subscribe to the podcast (returns to list view)
- Press `u` to unsubscribe from the podcast (returns to list view)
- Press `x` or `Esc` to return to the list view
- Subscription status is indicated by color (green for subscribed) and `[subscribed]` suffix
- When invoked from `list subscriptions`, unsubscribing returns to the list with the podcast removed

### Config Management
- `config` command shows editable key-value list.
- Changes persist immediately to YAML.

### Download Behavior
- Uses `/tmp` for partials (configurable).
- Resumes partials on retry.
- Prompts on overwrite only if hash differs.
- Logs all download start, success, and errors.

---

## Constraints & Assumptions
- **Language:** Go 1.22+
- **Frameworks/Libraries:**
  - UI: Bubble Tea, Bubbles, Lip Gloss, Survey
  - DB: SQLite (via modernc.org/sqlite)
  - Network: standard net/http
  - Config: gopkg.in/yaml.v3
- **Platforms:** linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
- **Distribution:** GitHub releases (tar/zip + checksums)

---

## Acceptance Criteria

### Startup
- Prompts for target dir if config missing.
- Creates config and DB automatically.

### Search
- `search "term"` completes within 5s using iTunes API.
- Interactive list displays results with subscription status.
- Pressing `Enter` on a result shows podcast details.
- Pressing `s` subscribes to the podcast:
  - From list view: stays in list view
  - From details view: returns to list view
- Pressing `u` unsubscribes from the podcast:
  - From list view: stays in list view
  - From details view: returns to list view
- Subscription status is visually indicated by color and `[subscribed]` suffix.
- Details view shows full podcast information including description.

### Subscriptions
- `list subscriptions` opens the interactive list view with all subscribed podcasts.
- Subscribing/unsubscribing from either the search results or subscriptions list updates the database immediately and reflects in the UI without requiring podcast IDs.

### Episodes
- `episodes [podcast]` lists correct items with states.
- `queue` transitions episode to `QUEUED`.
- Successful download → `DOWNLOADED`.
- Ignore/unignore toggles `IGNORED`/`SEEN`.

### OPML Import/Export
- `podsink --export-opml <path>` writes subscriptions to the specified file and exits before launching the REPL.
- `podsink --import-opml <path>` imports subscriptions, reporting counts of imported, skipped, and failed entries, then exits before launching the REPL.
- Passing both flags together returns an error and a non-zero exit code without performing any action.

### Downloads
- No leftover partials in final dir on error.
- Resume works across restarts.
- Prompt only when file differs by hash.

### Config
- Config changes via UI persist and take effect next run.

### Logging & Errors
- Logs include command name, success/failure, duration.
- Errors shown clearly in REPL; no panics or crashes.

---

## Delivery & Milestones

| Milestone | Focus | Deliverables |
|------------|--------|--------------|
| **M1 – Core CLI & Config** | Implement REPL shell, YAML config bootstrap/edit, SQLite setup. | `config`, `init`, `exit` commands functional. |
| **M2 – Discovery & Subscriptions** | Integrate iTunes search API, parse RSS feeds, interactive subscribe/unsubscribe flows. | `search`, `list subscriptions`. |
| **M3 – Episode Management** | Implement episode listing, state transitions, ignore logic. | `episodes`, `ignore`, state persistence. |
| **M4 – Downloader** | Add concurrent resumable downloads, retry/backoff, overwrite logic. | `queue`, `download`, logging. |
| **M5 – Polish & Production** | Autocomplete, help, packaging, release pipeline. | GitHub releases, docs, final binaries. |

---

## Success Metrics
- Command latency < 100 ms under normal conditions.
- End-to-end search→download flow completes without error.
- Persistent data remains consistent across sessions.
- Verified operation on Linux, macOS, and Windows.
