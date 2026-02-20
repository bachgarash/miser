# Miser

A local reverse-proxy that sits between your AI coding tool and the Anthropic API, tracking every token and dollar in a real-time terminal dashboard.

```
Cursor / Windsurf / etc.
        │  (OpenAI-compatible format)
        ▼
  localhost:8080  ◄── miser (translates, tracks tokens + cost)
        │  (Anthropic native format)
        ▼
 api.anthropic.com
```

## Why

Anthropic bills per token. When you vibe-code with an AI assistant, costs add up fast and you have zero visibility into what each request actually costs. Miser gives you that visibility without changing your workflow — it's a transparent proxy your tool talks to instead of the real API.

- **Real-time TUI dashboard** — k9s-style tables showing per-request and per-model cost breakdowns
- **OpenAI-to-Anthropic translation** — works with any tool that supports an OpenAI base URL override (Cursor, Windsurf, etc.)
- **Cache-aware pricing** — tracks cache read/write tokens separately for accurate cost calculation
- **Zero config required** — sensible defaults with built-in pricing for all current Claude models
- **CSV export** — dump your session data for spreadsheets or further analysis

## Quick Start

### Prerequisites

- **Go 1.22+**
- An **Anthropic API key** ([console.anthropic.com](https://console.anthropic.com) → Settings → API Keys)

### Build

```bash
git clone https://github.com/youruser/miser.git
cd miser
make build
```

Or without Make:

```bash
go build -o miser .
```

### Run

```bash
./miser
```

This starts the proxy on `localhost:8080` and opens the TUI dashboard.

### Point Cursor at it

Cursor doesn't expose an "Anthropic Base URL" override, but it does let you override the **OpenAI** base URL. Miser handles this by accepting OpenAI-format requests on `/v1/chat/completions`, translating them to Anthropic's native format, forwarding to `api.anthropic.com`, and translating the response back — fully transparent.

1. Open **Cursor Settings** → **Models**
2. Under **OpenAI API Key**, paste your Anthropic key (`sk-ant-...`)
3. Check **Override OpenAI Base URL** and set it to:
   ```
   http://localhost:8080/v1
   ```
4. In the model picker, select any Claude model (or type the model name manually, e.g. `claude-sonnet-4-20250514`)
5. Use Cursor normally — every request now flows through Miser

Switch to the terminal running Miser and you'll see requests, token counts, and costs appearing in real time.

> **Note:** Miser also accepts native Anthropic requests on `/v1/messages` for tools that support a custom Anthropic base URL directly.

## TUI Dashboard

The dashboard refreshes every 500ms and shows two tables:

| Section | What it shows |
|---|---|
| **Models** | Aggregate stats per model — request count, input/output tokens, cache tokens, total cost, and cost percentage |
| **Request Log** | Individual requests (newest first) — timestamp, model, tokens, cost, latency, HTTP status |

A summary bar at the top shows running totals across all models.

### Keyboard Shortcuts

| Key | Action |
|---|---|
| `q` | Quit |
| `c` | Clear session data |
| `e` | Export session to CSV |
| `Tab` | Switch focus between tables |
| `↑` `↓` | Scroll through rows |

## Configuration

### Generate a config file

```bash
./miser init                              # writes ./miser.toml
./miser init -o ~/.config/miser/config.toml  # custom path
./miser init --force                      # overwrite existing file
```

### Config file search order

1. `./miser.toml` (project-local)
2. `~/.config/miser/config.toml` (user-level)

Or pass an explicit path: `miser -c /path/to/config.toml`

### Example `miser.toml`

```toml
[proxy]
port    = 8080
target  = "https://api.anthropic.com"
timeout = "5m"

[models.claude-sonnet-4-20250514]
aliases           = ["claude-sonnet-4"]
input_per_mtok    = 3.00
output_per_mtok   = 15.00
cache_read_per_mtok  = 0.30
cache_write_per_mtok = 3.75

[models.claude-3-5-haiku-20241022]
aliases           = ["claude-3-5-haiku"]
input_per_mtok    = 0.80
output_per_mtok   = 4.00
cache_read_per_mtok  = 0.08
cache_write_per_mtok = 1.00

# Add new models here as Anthropic releases them — no rebuild needed.

[fallback]
input_per_mtok    = 3.00
output_per_mtok   = 15.00
cache_read_per_mtok  = 0.30
cache_write_per_mtok = 3.75
```

### Config precedence (lowest → highest)

```
built-in defaults → config file → environment variables → CLI flags
```

### Environment variables

| Variable | Equivalent flag | Example |
|---|---|---|
| `MISER_CONFIG` | `--config` | `MISER_CONFIG=~/my.toml miser` |
| `MISER_PORT` | `--port` | `MISER_PORT=9090 miser` |
| `MISER_TARGET` | `--target` | `MISER_TARGET=https://... miser` |

## CLI Reference

```
Usage:
  miser [flags]
  miser [command]

Commands:
  init        Generate a default miser.toml config file
  version     Print version information
  completion  Generate shell completion scripts (bash, zsh, fish, powershell)

Flags:
  -c, --config string   config file path [$MISER_CONFIG]
  -p, --port int        proxy listen port [$MISER_PORT]
  -t, --target string   upstream API base URL [$MISER_TARGET]
      --headless        run proxy without TUI (daemon / CI mode)
  -h, --help            help for miser
```

### Headless mode

Run the proxy without the TUI — useful for running as a background daemon or in CI. Each request is logged as a single line to stderr:

```bash
./miser --headless
# 14:23:01  claude-sonnet-4         12.4K in   2.1K out    $0.0690   1.4s  200
# 14:22:45  claude-3-5-haiku         8.2K in   1.8K out    $0.0138   0.9s  200
```

### Shell completions

```bash
# zsh
miser completion zsh > "${fpath[1]}/_miser"

# bash
miser completion bash > /etc/bash_completion.d/miser

# fish
miser completion fish > ~/.config/fish/completions/miser.fish
```

## How It Works

Miser exposes two endpoints:

| Endpoint | Format | Use case |
|---|---|---|
| `/v1/chat/completions` | OpenAI-compatible | Cursor, Windsurf, any OpenAI-SDK tool |
| `/v1/messages` | Anthropic native | Tools with Anthropic base URL support |

### OpenAI-compatible flow (`/v1/chat/completions`)

1. Your tool sends an OpenAI-format request with `Authorization: Bearer sk-ant-...`
2. Miser extracts system messages, maps fields, and converts to Anthropic's `/v1/messages` format
3. Forwards to `api.anthropic.com` with the key in `x-api-key`
4. Translates the Anthropic response back to OpenAI format
5. For streaming: converts Anthropic SSE events (`message_start`, `content_block_delta`, `message_delta`) to OpenAI SSE chunk format in real time
6. Tracks token counts and cost from the Anthropic usage data

### Native Anthropic flow (`/v1/messages`)

1. Request is forwarded verbatim — all headers pass through unchanged
2. Response is piped through unchanged
3. Token usage is extracted from the response body or SSE events for tracking

In both cases, your API key is never logged or saved.

## Built-in Model Pricing

Miser ships with current pricing for all Claude models ($ per 1M tokens):

| Model | Input | Output | Cache Read | Cache Write |
|---|---|---|---|---|
| Claude Sonnet 4 | $3.00 | $15.00 | $0.30 | $3.75 |
| Claude Opus 4 | $15.00 | $75.00 | $1.50 | $18.75 |
| Claude 3.7 Sonnet | $3.00 | $15.00 | $0.30 | $3.75 |
| Claude 3.5 Sonnet | $3.00 | $15.00 | $0.30 | $3.75 |
| Claude 3.5 Haiku | $0.80 | $4.00 | $0.08 | $1.00 |
| Claude 3 Opus | $15.00 | $75.00 | $1.50 | $18.75 |

Unknown models fall back to Sonnet-tier pricing. Override any of these via the config file.

## Project Structure

```
miser/
├── main.go                      Entry point
├── cmd/
│   ├── root.go                  CLI setup, config resolution, proxy startup
│   ├── init.go                  `miser init` — config file generator
│   ├── version.go               `miser version` — build info
│   └── default.toml             Embedded default config template
├── internal/
│   ├── config/config.go         TOML config loading with file discovery
│   ├── proxy/
│   │   ├── proxy.go             HTTP server, native Anthropic proxying, streaming
│   │   └── openai.go            OpenAI ↔ Anthropic request/response translation
│   ├── tracker/
│   │   ├── tracker.go           Thread-safe request recording and aggregation
│   │   └── pricing.go           Per-model cost calculation with alias resolution
│   └── tui/app.go               Terminal UI (tview) with live-refreshing tables
├── Makefile                     Build with version injection via ldflags
└── go.mod
```

## Roadmap

- [x] OpenAI-compatible endpoint support
- [ ] Context deduplication — hash file contents, replace repeated blocks with cached references
- [ ] Model routing — classify prompt complexity and auto-select cheaper models when appropriate
- [ ] Prompt compression — strip excessive whitespace, truncate stack traces, summarize repeated files
- [ ] Persistent history — save session data across restarts
- [ ] Budget alerts and per-session spend limits

## License

MIT
