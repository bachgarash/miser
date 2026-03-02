# Miser

A local reverse-proxy that sits between your AI coding tool and the Anthropic API, tracking every token and dollar in a real-time terminal dashboard.

```
Claude Code / Cursor / Windsurf / etc.
        │
        ▼
  localhost:8080  <── miser (tracks tokens + cost, optionally compresses prompts)
        │
        ▼
 api.anthropic.com
```

## Why

Anthropic bills per token. When you vibe-code with an AI assistant, costs add up fast and you have zero visibility into what each request actually costs. Miser gives you that visibility without changing your workflow — it's a transparent proxy your tool talks to instead of the real API.

- **Real-time TUI dashboard** — k9s-style tables showing per-request and per-model cost breakdowns
- **Prompt compression** — strip whitespace, deduplicate stack traces and repeated messages to reduce input tokens
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

## Setting Up with Claude Code

Claude Code talks directly to the Anthropic API, so it works with miser's native `/v1/messages` endpoint.

1. Start miser:
   ```bash
   ./miser
   ```

2. Configure Claude Code to use the proxy by setting the API base URL:
   ```bash
   ANTHROPIC_BASE_URL=http://localhost:8080 claude
   ```

   Or set it permanently in your shell profile:
   ```bash
   export ANTHROPIC_BASE_URL=http://localhost:8080
   ```

3. Use Claude Code normally — every request now flows through miser and you'll see tokens, cost, and latency in real time.

## Setting Up with Cursor

Cursor doesn't expose an "Anthropic Base URL" override, but it does let you override the **OpenAI** base URL. Miser handles this by accepting OpenAI-format requests on `/v1/chat/completions`, translating them to Anthropic's native format, forwarding to `api.anthropic.com`, and translating the response back — fully transparent.

1. Open **Cursor Settings** → **Models**
2. Under **OpenAI API Key**, paste your Anthropic key (`sk-ant-...`)
3. Check **Override OpenAI Base URL** and set it to:
   ```
   http://localhost:8080/v1
   ```
4. In the model picker, select any Claude model (or type the model name manually, e.g. `claude-sonnet-4-6`)
5. Use Cursor normally — every request now flows through miser

> **Note:** Any tool that supports a custom OpenAI or Anthropic base URL can be pointed at miser the same way.

## TUI Dashboard

The dashboard refreshes every 500ms and shows two tables:

| Section | What it shows |
|---|---|
| **Models** | Aggregate stats per model — request count, input/output tokens, cache tokens, total cost, and cost percentage |
| **Request Log** | Individual requests (newest first) — timestamp, model, tokens, cost, compression savings, latency, HTTP status |

A summary bar at the top shows running totals across all models, including overall compression savings when compression is enabled.

### Keyboard Shortcuts

| Key | Action |
|---|---|
| `q` | Quit |
| `c` | Clear session data |
| `e` | Export session to CSV |
| `Tab` | Switch focus between tables |
| `↑` `↓` | Scroll through rows |

## Prompt Compression

AI coding tools often send bloated prompts — repeated stack traces, duplicate file contents, excessive blank lines. Since miser sits between the tool and the API, it can transparently compress prompts before forwarding, reducing input tokens and saving money without changing any tool's workflow.

All compression layers are **off by default** — opt in via config. If any compression step errors, the original body is forwarded unmodified (fail-open).

### Compression Layers

| Layer | Config key | What it does |
|---|---|---|
| **Whitespace** | `whitespace` | Trims trailing spaces/tabs, collapses 3+ consecutive blank lines to 2. Leading indentation preserved. |
| **Stack truncation** | `stack_truncation` | Detects Go, Python, Node.js, and Java stack traces. First occurrence kept in full, duplicates replaced with `[... N similar stack frames omitted]`. |
| **Deduplication** | `deduplication` | Hashes message content (SHA-256). Identical messages replaced with `[Content identical to message #N ...]`. Only messages ≥ `min_block_size` bytes. |

### Enable Compression

Add to your `miser.toml`:

```toml
[compression]
whitespace       = true
stack_truncation = true
deduplication    = true
min_block_size   = 256
```

When compression is active, the TUI stats bar shows the overall compression percentage and each request row has a "SAVED" column. Headless mode appends `(compressed N%)` to log lines.

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

[compression]
whitespace       = true
stack_truncation = true
deduplication    = true
min_block_size   = 256

[models.claude-opus-4-6]
input_per_mtok    = 5.00
output_per_mtok   = 25.00
cache_read_per_mtok  = 0.50
cache_write_per_mtok = 6.25

[models.claude-haiku-4-5-20251001]
aliases           = ["claude-haiku-4-5"]
input_per_mtok    = 1.00
output_per_mtok   = 5.00
cache_read_per_mtok  = 0.10
cache_write_per_mtok = 1.25

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
# 14:23:01  claude-opus-4-6           12.4K in   2.1K out    $0.0935   1.4s  200  (compressed 12%)
# 14:22:45  claude-haiku-4-5           8.2K in   1.8K out    $0.0172   0.9s  200
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
| `/v1/messages` | Anthropic native | Claude Code, any tool with Anthropic base URL support |
| `/v1/chat/completions` | OpenAI-compatible | Cursor, Windsurf, any OpenAI-SDK tool |

### Native Anthropic flow (`/v1/messages`)

1. Request is forwarded to upstream — all headers pass through unchanged
2. If compression is enabled, prompt text is compressed before forwarding
3. Response is piped through unchanged
4. Token usage is extracted from the response body or SSE events for tracking

### OpenAI-compatible flow (`/v1/chat/completions`)

1. Your tool sends an OpenAI-format request with `Authorization: Bearer sk-ant-...`
2. Miser extracts system messages, maps fields, and converts to Anthropic's `/v1/messages` format
3. If compression is enabled, prompt text is compressed before forwarding
4. Forwards to `api.anthropic.com` with the key in `x-api-key`
5. Translates the Anthropic response back to OpenAI format
6. For streaming: converts Anthropic SSE events to OpenAI SSE chunk format in real time
7. Tracks token counts and cost from the Anthropic usage data

In both cases, your API key is never logged or saved.

## Built-in Model Pricing

Miser ships with current pricing for all Claude models ($ per 1M tokens):

| Model | Input | Output | Cache Read | Cache Write |
|---|---|---|---|---|
| Claude Opus 4.6 | $5.00 | $25.00 | $0.50 | $6.25 |
| Claude Sonnet 4.6 | $3.00 | $15.00 | $0.30 | $3.75 |
| Claude Haiku 4.5 | $1.00 | $5.00 | $0.10 | $1.25 |
| Claude Opus 4.5 | $5.00 | $25.00 | $0.50 | $6.25 |
| Claude Sonnet 4.5 | $3.00 | $15.00 | $0.30 | $3.75 |
| Claude Opus 4.1 | $15.00 | $75.00 | $1.50 | $18.75 |
| Claude Sonnet 4 | $3.00 | $15.00 | $0.30 | $3.75 |
| Claude Opus 4 | $15.00 | $75.00 | $1.50 | $18.75 |
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
│   ├── compress/
│   │   ├── compress.go          Types, config, and compression orchestrator
│   │   ├── whitespace.go        Whitespace normalization layer
│   │   ├── stacks.go            Stack trace deduplication layer
│   │   ├── dedup.go             Message deduplication layer
│   │   └── compress_test.go     Tests for all compression layers
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
- [x] Prompt compression — strip whitespace, truncate stack traces, deduplicate messages
- [ ] Model routing — classify prompt complexity and auto-select cheaper models when appropriate
- [ ] Persistent history — save session data across restarts
- [ ] Budget alerts and per-session spend limits

## License

MIT
