# Miser

A local proxy that sits between your AI coding tool and the Anthropic API,
tracking every token and dollar in a real-time k9s-style terminal dashboard.

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

Anthropic bills per token. When you vibe-code with an AI assistant, costs
add up fast and you have zero visibility into what each request actually
costs. Miser gives you that visibility without changing your workflow — it's
a transparent proxy your tool talks to instead of the real API.

## Quick start

### Prerequisites

- **Go 1.22+**
- An **Anthropic API key** (get one at [console.anthropic.com](https://console.anthropic.com) → Settings → API Keys)

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

Cursor doesn't expose an "Anthropic Base URL" override, but it does let you
override the **OpenAI** base URL. Miser handles this by accepting
OpenAI-format requests on `/v1/chat/completions`, translating them to
Anthropic format, forwarding to `api.anthropic.com`, and translating the
response back — fully transparent.

1. Open **Cursor Settings** → **Models**
2. Under **OpenAI API Key**, paste your Anthropic key (`sk-ant-...`)
3. Check **Override OpenAI Base URL** and set it to:
   ```
   http://localhost:8080/v1
   ```
4. In the model picker, select any Claude model (or type the model name
   manually, e.g. `claude-sonnet-4-20250514`)
5. Use Cursor normally — every request now flows through miser

Switch to the terminal running miser and you'll see requests, token counts,
and costs appearing in real time.

> **Note:** Miser also accepts native Anthropic requests on `/v1/messages`
> for tools that support a custom Anthropic base URL directly.

## TUI keyboard shortcuts

| Key       | Action                              |
|-----------|-------------------------------------|
| `q`       | Quit                                |
| `c`       | Clear session data                  |
| `e`       | Export session to CSV               |
| `Tab`     | Switch focus between tables         |
| `↑` `↓`   | Scroll through rows                 |

## Configuration

### Generate a config file

```bash
./miser init                    # writes ./miser.toml
./miser init -o ~/.config/miser/config.toml   # custom path
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

| Variable        | Equivalent flag | Example                            |
|-----------------|-----------------|------------------------------------|
| `MISER_CONFIG`  | `--config`      | `MISER_CONFIG=~/my.toml miser`     |
| `MISER_PORT`    | `--port`        | `MISER_PORT=9090 miser`            |
| `MISER_TARGET`  | `--target`      | `MISER_TARGET=https://... miser`   |

## CLI reference

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

Run the proxy without the TUI — useful for running as a background daemon or
in CI. Each request is logged as a single line to stderr:

```bash
./miser --headless
# 14:23:01  claude-sonnet-4         12.4K in   2.1K out    $0.0690   1.4s  200
# 14:22:45  claude-sonnet-4          8.2K in   1.8K out    $0.0516   0.9s  200
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

## How it works

Miser exposes two endpoints:

| Endpoint | Format | Use case |
|---|---|---|
| `/v1/chat/completions` | OpenAI-compatible | Cursor, Windsurf, any OpenAI-SDK tool |
| `/v1/messages` | Anthropic native | Tools with Anthropic base URL support |

**OpenAI-compatible flow** (`/v1/chat/completions`):

1. Your tool sends an OpenAI-format request with `Authorization: Bearer sk-ant-...`
2. Miser translates it to Anthropic format (extracts system messages, maps fields)
3. Forwards to `api.anthropic.com/v1/messages` with the key in `x-api-key`
4. Translates the Anthropic response back to OpenAI format
5. For streaming: converts Anthropic SSE events to OpenAI SSE chunk format in real time
6. Tracks token counts and cost from the Anthropic usage data

**Native Anthropic flow** (`/v1/messages`):

1. Request is forwarded verbatim (all headers pass through unchanged)
2. Response is piped through unchanged
3. Token usage is extracted from the response/SSE events for tracking

In both cases, your API key is never logged or saved. The TUI refreshes
every 500ms with the latest cost data.

## Roadmap

- [ ] Context deduplication — hash file contents, replace repeated blocks with cached references
- [ ] Model routing — classify prompt complexity and auto-select cheaper models when appropriate
- [ ] Prompt compression — strip excessive whitespace, truncate stack traces, summarize repeated files
- [ ] Persistent history — save session data across restarts
- [x] OpenAI-compatible endpoint support
- [ ] Budget alerts and per-session spend limits

## License

MIT
