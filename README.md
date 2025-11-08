# Podsink

**Podsink** is a command-line tool for discovering, subscribing to, and downloading podcast episodes. It provides a menu-driven interactive interface with persistent state management via SQLite.

## Features

- **Search & Subscribe**: Find podcasts using the iTunes Search API
- **Episode Management**: View, queue, and download episodes with state tracking
- **Concurrent Downloads**: Configurable parallel downloads with retry logic and resume support
- **State Auto-Correction**: Automatically fixes episodes stuck in QUEUED state on startup
- **Dangling File Detection**: Identifies files in download directory not tracked in database
- **OPML Support**: Import and export subscriptions for portability
- **Data Integrity**: SHA256 hash verification prevents unnecessary re-downloads
- **Secure**: HTTPS-only with TLS verification, optional proxy support
- **Interactive CLI**: Navigable menu interface with keyboard shortcuts and live counts
- **Persistent Storage**: SQLite database with automatic schema management
- **Rotating Logs**: Structured logging with automatic rotation (10 MB × 3 files)

## Requirements

- **Go**: 1.22 or higher
- **Operating Systems**: Linux, macOS, Windows

## Installation

### Build from Source

```bash
git clone <repository-url>
cd podsink
go build -o podsink ./cmd/podsink
```

### Install Directly

```bash
go install ./cmd/podsink@latest
```

## Quick Start

### First Run

On first run, Podsink will prompt you to configure the download directory and create necessary files in `~/.podsink/`:

```bash
./podsink
```

Configuration files created:
- `~/.podsink/config.yaml` - Application configuration
- `~/.podsink/app.db` - SQLite database
- `~/.podsink/podsink.log` - Application logs

### Basic Usage

The application starts with a navigable main menu:

```
Podsink - Podcast Manager
Use ↑↓/jk to navigate, Enter to select, [p]odcasts [e]pisodes [q]ueue [d]ownloads [c]onfig, ESC/[x] to exit

  → [p] podcasts
    [e] episodes
    [q] queue
    [d] downloads
    [c] config [show]
    [x] exit
```

**Navigation:**
- Use ↑↓ or j/k to move through menu items
- Press Enter or use keyboard shortcuts (p/e/q/d/c/x) to select an option
- Press ESC to exit from any submenu back to the main menu. When viewing podcast or episode search results, press `x` to return to the respective list; otherwise `x` also exits to the main menu.

**List Subscriptions:** Press `p` or select "podcasts" to view all subscribed podcasts. While in this view, press `s` to open podcast search without returning to the main menu. Enter a query and press Enter to search; submit an empty query to return to the list.

**View Episodes:** Press `e` or select "episodes" to browse recent episodes. Press `s` at any time to search episodes by title or podcast:
```
Episodes (hiding ignored) (Newest First) - showing 1-12 of 147:
Use ↑↓/jk to navigate, Enter for details, [i] ignore, [A] all, [I] ignored, [D] downloaded, [d] download, [x] return, Esc to exit

  → 2025-01-15 Go Time          Building Better Go APIs                45.2 MB
    2025-01-08 Go Time          Concurrency Patterns                    52.8 MB
```
Navigate with ↑↓/jk; press `i` to ignore; `[A]` to show all, `[I]` for ignored only, `[D]` for downloaded only; `d` to queue for download.

**View Queue:** Press `q` or select "queue" to see download queue:
```
Download Queue - 2 episode(s)
Use ↑↓/jk to navigate, [x]/Esc to return to main menu

  → 2025-01-15 Go Time          Building Better Go APIs                Queued
    2025-01-08 Go Time          Concurrency Patterns                    Queued
```

To import or export subscriptions without entering the interactive menu, use the command-line
flags `--import-opml <file>` or `--export-opml <file>`.

## Menu Reference

### Main Menu Options

- **Podcasts** `[p]` - Browse all subscriptions
  - View all subscribed podcasts with episode counts
  - Navigate with ↑↓/jk
  - Press Enter for podcast details
  - Press `u` to unsubscribe
  - Press `s` to search for new podcasts from within the subscriptions list (press Enter to search; submit blank to cancel)
  - While viewing podcast search results, press `x` to return to the subscriptions list; press ESC to exit to the main menu
  - Press `x` or ESC to return to the main menu when not viewing search results

- **Episodes** `[e]` - Browse all recorded episodes (newest first)
  - Navigate with ↑↓/jk
  - Press Enter to view episode details
  - Press `i` to ignore/unignore an episode
  - Press `[A]` to show all episodes
  - Press `[I]` to show only ignored episodes
  - Press `[D]` to show only downloaded episodes
  - Press `d` to queue episode for download
  - Press `s` to search episodes by title or podcast (press Enter to search; submit blank to cancel)
  - While viewing episode search results, press `x` to return to the full episode list; press ESC to exit to the main menu
  - Press `x` or ESC to return to the main menu when not viewing search results
  - Episodes displayed as: DATE | PODCAST_NAME | EPISODE_TITLE | SIZE (MB)

- **Queue** `[q]` - View download queue
  - Shows both queued and downloaded episodes (until explicitly removed)
  - Displays count of queued episodes in main menu (e.g., "queue (3)")
  - Navigate with ↑↓/jk
  - Displays status (queued, error with retry count)
  - Press `x` or ESC to return to main menu

- **Downloads** `[d]` - View all downloaded episodes
  - Shows episodes with DOWNLOADED or DELETED state
  - Automatically detects and marks episodes with missing files as DELETED
  - Displays count of downloaded episodes in main menu (e.g., "downloads (15)")
  - Shows dangling files section: files in download directory not tracked in database
  - Navigate with ↑↓/jk
  - Deleted files are marked with [DELETED] indicator
  - Press `x` or ESC to return to main menu

- **Config** `[c]` - Configuration management
  - Interactive configuration editor
  - Modify settings like download directory, parallel downloads, themes, etc.

- **Exit** `[x]` - Exit the application

### Import/Export (Command-line only)

- `--import-opml <file_path>` - Import subscriptions from OPML file without starting the menu
- `--export-opml <file_path>` - Export subscriptions to OPML format without starting the menu

## Configuration

Edit `~/.podsink/config.yaml` or use the `config` command:

```yaml
download_root: /path/to/podcasts        # Where episodes are saved
parallel_downloads: 4                    # Concurrent downloads (0 = disabled)
tmp_dir: /tmp                           # Temporary download directory
retry_count: 3                          # Download retry attempts
retry_backoff_max_seconds: 60           # Max backoff delay between retries
user_agent: podsink/1.0                 # Custom HTTP user agent
proxy: ""                               # HTTP proxy URL (optional)
tls_verify: true                        # Verify TLS certificates
color_theme: default                    # UI color theme (see available options below)
max_episodes: 12                        # Maximum episodes to display in list view
max_episode_description_lines: 12       # Description lines shown before scrolling in details view
podcast_name_max_length: 16             # Maximum characters for podcast name in episode list view
episode_name_max_length: 40             # Maximum characters for episode name in episode list view
```

Available themes:

- `default` — Balanced dark theme used historically
- `high_contrast` — Brighter accents and higher contrast for readability

## Episode States

Episodes progress through the following states:

- **NEW** - Newly discovered episode
- **SEEN** - Episode viewed in listings
- **IGNORED** - User has hidden the episode
- **QUEUED** - Queued for background download
- **DOWNLOADED** - Successfully downloaded
- **DELETED** - Downloaded but file no longer exists on filesystem

## Advanced Features

### Concurrent Downloads

Configure `parallel_downloads` to enable background downloading:
- Select **Config** from the main menu (press `c`)
- Set parallel_downloads to 4 (or your preferred number)

Queue multiple episodes and they'll download concurrently:
- Navigate to **Episodes** (press `e`)
- Use ↑↓/jk to select episodes
- Press `d` to queue each episode for download
- Downloads happen automatically in background workers

### Resume Support

Interrupted downloads are automatically resumed on retry using HTTP Range requests. Partial files are stored in your configured `tmp_dir`.

### Hash Verification

Downloaded files are SHA256-hashed and stored in the database. Re-downloading an episode will skip the download if the existing file has the same hash.

### OPML Portability

Export your subscriptions to share across devices or podcast apps:

```bash
./podsink --export-opml ~/my-podcasts.opml
Exported 25 subscriptions to /Users/you/my-podcasts.opml
```

Import from other podcast apps:

```bash
./podsink --import-opml ~/podcasts-backup.opml
Imported 18 subscriptions, skipped 7 already subscribed.
```

## Development

### Running Tests

```bash
go test ./...
```

### Run with Coverage

```bash
go test -cover ./...
```

### Code Quality

```bash
go fmt ./...
go vet ./...
```

### Build for Production

```bash
go build -ldflags "-s -w" -o podsink ./cmd/podsink
```

## Architecture

Podsink follows a clean layered architecture:

- **cmd/podsink** - Application entry point
- **internal/app** - Core business logic and command handlers
- **internal/config** - Configuration management
- **internal/repl** - Interactive menu interface (Bubble Tea)
- **internal/storage** - SQLite database layer
- **internal/feeds** - RSS feed parsing
- **internal/itunes** - iTunes Search API integration
- **internal/opml** - OPML import/export
- **internal/logging** - Structured logging with rotation

## Documentation

For detailed specifications, architecture decisions, and requirements, see [SPECIFICATION.md](SPECIFICATION.md).

## Security

- **HTTPS-only**: All network requests use HTTPS with strict TLS verification (configurable)
- **No telemetry**: Zero analytics, tracking, or external calls beyond iTunes API and podcast feeds
- **Secure storage**: Config and database files created with 0600/0700 permissions
- **Proxy support**: Configure HTTP proxy for network-restricted environments

## Troubleshooting

### Check Logs

```bash
tail -f ~/.podsink/podsink.log
```

### Reset Configuration

```bash
rm -rf ~/.podsink
./podsink  # Will recreate with defaults
```

### Download Issues

- Verify `download_root` is writable
- Check `tmp_dir` has sufficient space
- Ensure network connectivity and DNS resolution
- Review logs for specific error messages

### Database Corruption

```bash
# Backup first
cp ~/.podsink/app.db ~/.podsink/app.db.backup

# Reset
rm ~/.podsink/app.db
./podsink
# Re-import subscriptions if you have an OPML backup
```

## Performance

- **Startup**: < 1 second
- **Search latency**: Depends on iTunes API response time (~500ms typical)
- **Download throughput**: Limited only by network bandwidth and configured concurrency
- **Database**: Efficient for ~500 subscriptions and ~5,000 episodes

## License

[Add your license here]

## Contributing

[Add contribution guidelines here]

## Acknowledgments

Built with:
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - Terminal UI framework
- [Bubbles](https://github.com/charmbracelet/bubbles) - TUI components
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) - Style definitions
- [Survey](https://github.com/AlecAivazis/survey) - Interactive prompts
- [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) - Pure Go SQLite driver
- [html2text](https://github.com/jaytaylor/html2text) - HTML to plain text conversion
