# Podsink

**Podsink** is a command-line tool for discovering, subscribing to, and downloading podcast episodes. It provides a REPL-style interactive interface with persistent state management via SQLite.

## Features

- **Search & Subscribe**: Find podcasts using the iTunes Search API
- **Episode Management**: View, queue, and download episodes with state tracking
- **Concurrent Downloads**: Configurable parallel downloads with retry logic and resume support
- **OPML Support**: Import and export subscriptions for portability
- **Data Integrity**: SHA256 hash verification prevents unnecessary re-downloads
- **Secure**: HTTPS-only with TLS verification, optional proxy support
- **Interactive CLI**: Tab-completion for commands, intuitive REPL interface
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

Once in the REPL, you can use the following commands:

```
podsink> search golang
# An interactive list appears. Use ↑↓/jk to navigate results, Enter for details,
# press "s" to subscribe, or "u" to unsubscribe. The same view is used when
# browsing your subscriptions.

podsink> list subscriptions
# Shows all subscribed podcasts in the interactive list. Use "u" to unsubscribe
# from the highlighted podcast or Enter to view details.

podsink> episodes 12345
Episodes for Go Time (12345):
  ID         STATE  PUBLISHED   TITLE
  ep-001     NEW    2025-01-15  Building Better Go APIs
  ep-002     NEW    2025-01-08  Concurrency Patterns

podsink> download ep-001
Downloaded Building Better Go APIs to /path/to/downloads/Go_Time/Building_Better_Go_APIs.mp3

podsink> help
Available commands:
  help [command]              Show information about available commands
  config [show]               View or edit application configuration
  search <query>              Search for podcasts via the iTunes API
  list subscriptions          List all podcast subscriptions
  episodes <podcast_id>       View episodes for a podcast
  queue <episode_id>          Queue an episode for download
  download <episode_id>       Download an episode immediately
  ignore <episode_id>         Toggle the ignored state for an episode
  export <file_path>          Export subscriptions to OPML file
  import <file_path>          Import subscriptions from OPML file
  exit                        Exit the application
```

## Commands Reference

### Discovery & Subscription Management

- `search <query>` - Search for podcasts using iTunes API (interactive results support subscribing/unsubscribing)
- `list subscriptions` - Browse all subscriptions in the interactive list (unsubscribe directly or open details)

### Episode Management

- `episodes <podcast_id>` - List all episodes for a subscribed podcast
- `queue <episode_id>` - Add episode to download queue (background download)
- `download <episode_id>` - Download episode immediately (foreground)
- `ignore <episode_id>` - Toggle ignore state (hidden from listings)

### Import/Export

- `export <file_path>` - Export subscriptions to OPML format
- `import <file_path>` - Import subscriptions from OPML file

### Configuration

- `config` - Interactive configuration editor
- `config show` - Display current configuration

### General

- `help [command]` - Show help for all commands or a specific command
- `exit` (or `quit`) - Exit the application

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
```

## Episode States

Episodes progress through the following states:

- **NEW** - Newly discovered episode
- **SEEN** - Episode viewed in listings
- **IGNORED** - User has hidden the episode
- **QUEUED** - Queued for background download
- **DOWNLOADED** - Successfully downloaded

## Advanced Features

### Concurrent Downloads

Configure `parallel_downloads` to enable background downloading:

```bash
podsink> config
# Set parallel_downloads to 4 (or your preferred number)
```

Queue multiple episodes and they'll download concurrently:

```bash
podsink> queue ep-001
podsink> queue ep-002
podsink> queue ep-003
# Downloads happen in background workers
```

### Resume Support

Interrupted downloads are automatically resumed on retry using HTTP Range requests. Partial files are stored in your configured `tmp_dir`.

### Hash Verification

Downloaded files are SHA256-hashed and stored in the database. Re-downloading an episode will skip the download if the existing file has the same hash.

### OPML Portability

Export your subscriptions to share across devices or podcast apps:

```bash
podsink> export ~/my-podcasts.opml
Exported 25 subscriptions to /Users/you/my-podcasts.opml
```

Import from other podcast apps:

```bash
podsink> import ~/podcasts-backup.opml
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
- **internal/repl** - Interactive REPL interface (Bubble Tea)
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
