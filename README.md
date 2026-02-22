# DragonScale

DragonScale is a compact AI agent runtime for Linux and embedded environments, focused on
memory-aware context management, secure tool execution, and practical local-first deployment.

DragonScale diverges from upstream with its own architectural decisions:
 
- vendored LLM SDK
- MemGPT-style tiered memory
- Isolated Tool Runtime: 
  - capability-based security
  - DAG-based parallel tool execution
  - libSQL-native storage with vector search

> [!NOTE] 
> Where it makes sense, upstream changes are merged back. This is not a strict rule, but a guideline.

---

## Why DragonScale Exists

The original project is a solid foundation — a single-binary AI agent designed for constrained devices. But it has architectural gaps that limit extensibility:

- **No privilege boundary** between the LLM and tool execution — a compromised tool has full process access
- **No structured memory** beyond flat markdown files
- **All tools loaded into context** every request (token waste at scale)
- **Sequential tool calling only** — each tool call requires a full inference pass
- **Hand-rolled LLM provider implementations** with no streaming, retry, or multi-provider support

This project addresses those gaps while preserving strengths: a small binary, low memory footprint, and single-process deployment.

## Architecture

```mermaid
flowchart TB
    subgraph AgentLoop["Agent Loop"]
        AL[AgentLoop] --> CB[ContextBuilder]
        AL --> TR[ToolRegistry]
        AL --> MS[MemoryStore]
        AL --> FSM[ReAct FSM]
    end

    subgraph ITR["Isolated Tool Runtime"]
        SB[SecureBus] --> CAP[Capability Check]
        SB --> SI[Secret Injection]
        SB --> EX[Tool Execution]
        SB --> LS[Leak Scanning]
        SB --> AU[Audit Log]
    end

    subgraph DAG["DAG Executor"]
        DE[Executor] --> RES[Dependency Resolver]
        DE --> WAVE[Parallel Wave Dispatch]
        DE --> JOIN[Joiner Synthesis]
        RT[Router] -->|ModeReAct| FSM
        RT -->|ModeDAG| DE
    end

    subgraph Fantasy["Fantasy SDK (vendored)"]
        FA[FantasyAdapter] --> FP[Provider Registry]
        FP --> OR[OpenRouter / Anthropic / OpenAI / Gemini]
    end

    subgraph Progressive["Progressive Disclosure"]
        TR --> TS["tool_search (fuzzy)"]
        TR --> TC["tool_call (dispatch)"]
    end

    subgraph Memory["Memory System"]
        MS --> WC[Working Context]
        MS --> RC[Recall Memory]
        MS --> AR[Archival Memory]
        MS --> OBS[Observational Memory]
        MS --> DAGC[DAG Compression]
    end

    subgraph Storage["libSQL Storage"]
        DEL[LibSQLDelegate] --> BLOB["BLOB PK (UUIDv7)"]
        DEL --> F32["F32_BLOB (embeddings)"]
        DEL --> FTS["FTS5 + BM25"]
        DEL --> VEC["vector_top_k (ANN)"]
    end

    subgraph Security["Security"]
        VLT[Vault XChaCha20] --> SS[SecretStore]
        SS --> KR[Keyring / Env / File]
        RED[Redactor] --> SB
        ZKP[Schnorr ZKP] -.-> SOCK[Daemon Socket]
    end

    subgraph Bus["Message Bus"]
        BUS[MessageBus] --> TG[Telegram]
        BUS --> DC[Discord]
        BUS --> SL[Slack]
        BUS --> LN[LINE]
        BUS --> DT[DingTalk]
        BUS --> QQ[QQ]
    end

    AL --> SB
    AL --> FA
    MS --> DEL
    AL --> BUS
    SB --> TR

    style AgentLoop fill:#1a1a2e,stroke:#e066ff,stroke-width:2px,color:#fff
    style ITR fill:#1a1a2e,stroke:#ff6b6b,stroke-width:2px,color:#fff
    style DAG fill:#1a1a2e,stroke:#ffab00,stroke-width:2px,color:#fff
    style Fantasy fill:#1a1a2e,stroke:#4d94ff,stroke-width:2px,color:#fff
    style Progressive fill:#1a1a2e,stroke:#00bfa5,stroke-width:2px,color:#fff
    style Memory fill:#1a1a2e,stroke:#2eb82e,stroke-width:2px,color:#fff
    style Storage fill:#1a1a2e,stroke:#9c27b0,stroke-width:2px,color:#fff
    style Security fill:#1a1a2e,stroke:#ff9800,stroke-width:2px,color:#fff
    style Bus fill:#1a1a2e,stroke:#607d8b,stroke-width:2px,color:#fff
```

### Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Isolated Tool Runtime** | All tool calls route through a `SecureBus` that enforces capability manifests, injects secrets, scans output for leaks, and writes audit logs. The LLM never sees raw secrets. See [ADR-001](docs/adr/001-isolated-tool-runtime.md). |
| **DAG executor** | LLMCompiler-style parallel tool dispatch. The planner builds a dependency DAG in a single inference pass; the executor dispatches independent nodes concurrently. Joiner synthesizes results. Replanning on failure. Falls back to ReAct for simple single-tool cases. |
| **Vendored Fantasy SDK** | `charm.land/fantasy` vendored into `internal/fantasy/` via `go.mod` replace directive. Enables direct modification for streaming hooks, tool call repair, and progressive disclosure. |
| **MemGPT + Observational Memory** | Working context (hot), recall items (warm), archival chunks (cold, embedded + indexed), plus observational memory (compressed conversation history with priority-tagged observations and temporal reasoning). DAG-based context budget compression manages token allocation across tiers. |
| **Progressive tool disclosure** | Agent sees only `tool_search` and `tool_call` meta-tools. Discovers actual tools on demand via fuzzy search. Cuts system prompt tokens for large registries. |
| **libSQL over modernc/sqlite** | Native F32_BLOB for vector storage, `libsql_vector_idx` for ANN search, FTS5 for full-text. Single database, no external vector DB dependency. |
| **BLOB primary keys** | 16-byte UUIDv7 stored as BLOB. Compact, byte-comparable, monotonically sortable by creation time. |
| **XChaCha20-Poly1305 vault** | Secrets encrypted at rest with AES-256-GCM or XChaCha20-Poly1305. Master key from OS keyring, env var, or file. Schnorr ZKP for daemon-mode authentication. |
| **Goose migrations** | Schema managed by `pressly/goose/v3`. 10 versioned migrations covering core schema, FTS5, vector indexes, KV store, documents, audit log, conversations, runtime state, jobs, and conversation graphs. |
| **FlatBuffers command protocol** | Zero-copy serialized `ToolRequest`/`ToolResponse` for the ITR command vocabulary. Same binary format across in-process channels, Unix sockets (daemon mode), and wazero WASM host calls. |

## Project Layout

```
cmd/dragonscale/              # CLI entrypoint
internal/fantasy/          # Vendored charm.land/fantasy SDK
docs/adr/                  # Architecture Decision Records
eval/                      # Promptfoo-based evaluation harness
pkg/
├── agent/                 # Agent loop, ReAct FSM, context builder
│   ├── conversations/     # Conversation store (multi-turn tracking)
│   ├── mentions/          # Mention tracking
│   └── threads/           # Thread store
├── auth/                  # OAuth2 + PKCE for provider auth
├── bus/                   # Hub-and-spoke message bus
├── cache/                 # Generic LRU+TTL cache (SWR, tag invalidation)
├── channels/              # Telegram, Discord, Slack, LINE, DingTalk, QQ, MaixCAM
├── config/                # JSON config with env var overrides, XDG paths
├── cron/                  # Cron scheduler (gronx-based)
├── devices/               # Hardware device hotplug (USB on Linux)
├── fantasy/               # Fantasy SDK adapter (provider factory, type conversion)
├── health/                # HTTP health/readiness endpoints
├── heartbeat/             # Periodic task execution
├── ids/                   # UUIDv7 generation + BLOB codec
├── itr/                   # Isolated Tool Runtime
│   ├── dag/               # DAG executor, planner, resolver, router, replanner
│   ├── itrfb/             # FlatBuffers generated code (command protocol)
│   └── wasm/              # wazero WASM isolate runtime + transport
├── logger/                # Structured logger
├── memory/                # Memory system
│   ├── dag/               # DAG-based context budget compression
│   ├── delegate/          # libSQL storage backend (FTS5, vector, capabilities)
│   ├── migrations/        # Goose versioned schema migrations (001–010)
│   ├── observation/       # Observational memory (observer, reflector, store)
│   ├── sqlc/              # sqlc config + generated code
│   └── store/             # MemoryStore, retrieval, chunking, scoring, queuing
├── messages/              # Canonical message/tool-call types
├── dserrors/               # Structured error types
├── rlm/                   # Recursive Language Model engine (rope, fanout, strategy)
├── security/              # Vault, SecretStore, Redactor, URL guard, Schnorr ZKP
│   └── securebus/         # SecureBus (policy, audit, transport, socket transport)
├── session/               # Session manager with LRU disk-backed cache
├── skills/                # Skill loader, installer, dependency graph, templates
├── tools/                 # Tool registry, meta-tools, built-in tools, CapableTool
├── voice/                 # Groq Whisper voice transcription
└── worker/                # Background job worker
skills/                    # Built-in skills (weather, tmux, summarize, github, hardware)
config/                    # Example configuration files
```

## Quick Start

### Build from source

> [!IMPORTANT]
> Requires `CGO_ENABLED=1` — the go-libsql driver links against glibc.
> Build/runtime target is Linux only (no macOS, no Windows, no musl).

```bash
git clone https://github.com/ZanzyTHEbar/dragonscale.git
cd dragonscale
make build
```


### Configure

```bash
./bin/dragonscale onboard
```

The onboard wizard initializes config, workspace, and optionally sets up encrypted secret storage.

Edit `~/.dragonscale/config.json`:

```json
{
  "agents": {
    "defaults": {
      "model": "anthropic/claude-sonnet-4-20250514",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20
    }
  },
  "providers": {
    "openrouter": {
      "api_key": "sk-or-v1-xxx",
      "api_base": "https://openrouter.ai/api/v1"
    }
  },
  "tools": {
    "progressive_disclosure": true,
    "web": {
      "duckduckgo": { "enabled": true, "max_results": 5 }
    }
  }
}
```

### Run

```bash
# One-shot
dragonscale agent -m "What is 2+2?"

# Interactive REPL
dragonscale agent

# Gateway (Telegram, Discord, etc.)
dragonscale gateway
```

### Docker

```bash
cp config/config.example.json config/config.json
docker compose --profile gateway up -d
docker compose logs -f dragonscale-gateway
```

## Secret Management

DragonScale encrypts secrets at rest with XChaCha20-Poly1305. The master key is sourced from an environment variable, OS keyring, or file.

```bash
dragonscale secret init           # Generate a master key
dragonscale secret add <name>     # Store a secret (interactive prompt)
dragonscale secret list            # List secret names
dragonscale secret delete <name>  # Remove a secret
```

> [!WARNING]
> This is a security-sensitive operation. The master key is used to encrypt and decrypt secrets. If you lose it, you will not be able to decrypt secrets.
> You should should NEVER store the master key in a file or environment variable if possible.


Set the master key: `export DRAGONSCALE_MASTER_KEY=<hex>`

Tools declare which secrets they need via `CapableTool.Capabilities()`. The SecureBus injects secrets into tool execution context at runtime — the LLM never sees them. Tool output is scanned for leaked patterns before it reaches the agent loop.

## Daemon Mode

For non-embedded deployments, the SecureBus can run in a separate privileged daemon process. The agent connects as an unprivileged client over a Unix domain socket.

```bash
dragonscale daemon start          # Start daemon (foreground, Ctrl+C to stop)
dragonscale daemon status         # Check if running
dragonscale daemon stop           # Stop a running daemon
```

## LLM Providers

The Fantasy SDK handles provider routing. Configure any supported provider:

| Provider | Config Key | Notes |
|----------|-----------|-------|
| OpenRouter | `openrouter` | Access to all models via single API key |
| Anthropic | `anthropic` | Claude direct |
| OpenAI | `openai` | GPT direct |
| Google Gemini | `gemini` | Gemini direct |
| Groq | `groq` | Fast inference + Whisper voice transcription |

API key links: [OpenRouter](https://openrouter.ai/keys) · [Anthropic](https://console.anthropic.com) · [OpenAI](https://platform.openai.com) · [Gemini](https://aistudio.google.com/api-keys) · [Groq](https://console.groq.com)

## Chat Channels

| Channel | Setup Complexity |
|---------|-----------------|
| Telegram | Easy — single bot token |
| Discord | Easy — bot token + message content intent |
| QQ | Easy — AppID + AppSecret |
| DingTalk | Medium — app credentials |
| LINE | Medium — credentials + webhook URL |
| Slack | Medium — app credentials + event subscriptions |

<details>
<summary><b>Telegram</b></summary>

1. Create bot via `@BotFather` on Telegram, copy token
2. Get your user ID from `@userinfobot`
3. Configure:

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allowFrom": ["YOUR_USER_ID"]
    }
  }
}
```

4. Run `dragonscale gateway`
</details>

<details>
<summary><b>Discord</b></summary>

1. Create application at https://discord.com/developers/applications
2. Create bot, copy token, enable MESSAGE CONTENT INTENT
3. Get your User ID (Developer Mode → right-click avatar → Copy User ID)
4. Configure:

```json
{
  "channels": {
    "discord": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allowFrom": ["YOUR_USER_ID"]
    }
  }
}
```

5. Invite bot: OAuth2 → URL Generator → Scopes: `bot` → Permissions: `Send Messages`, `Read Message History`
6. Run `dragonscale gateway`
</details>

<details>
<summary><b>QQ</b></summary>

1. Create application at [QQ Open Platform](https://q.qq.com/#)
2. Configure:

```json
{
  "channels": {
    "qq": {
      "enabled": true,
      "app_id": "YOUR_APP_ID",
      "app_secret": "YOUR_APP_SECRET",
      "allow_from": []
    }
  }
}
```

3. Run `dragonscale gateway`
</details>

<details>
<summary><b>DingTalk</b></summary>

1. Create internal app at [DingTalk Open Platform](https://open.dingtalk.com/)
2. Configure:

```json
{
  "channels": {
    "dingtalk": {
      "enabled": true,
      "client_id": "YOUR_CLIENT_ID",
      "client_secret": "YOUR_CLIENT_SECRET",
      "allow_from": []
    }
  }
}
```

3. Run `dragonscale gateway`
</details>

<details>
<summary><b>LINE</b></summary>

1. Create Messaging API channel at [LINE Developers Console](https://developers.line.biz/)
2. Configure:

```json
{
  "channels": {
    "line": {
      "enabled": true,
      "channel_secret": "YOUR_CHANNEL_SECRET",
      "channel_access_token": "YOUR_CHANNEL_ACCESS_TOKEN",
      "webhook_host": "0.0.0.0",
      "webhook_port": 18791,
      "webhook_path": "/webhook/line",
      "allow_from": []
    }
  }
}
```

3. Set up HTTPS webhook (e.g., `ngrok http 18791`) and configure the URL in LINE console
4. Run `dragonscale gateway`
</details>

## Memory System

DragonScale implements a multi-tier memory system combining MemGPT-style tiered storage with observational memory compression:

| Tier | Purpose | Storage | Search |
|------|---------|---------|--------|
| **Working Context** | Current focus, active goals | Single JSON document per agent | Direct load |
| **Recall Memory** | Session-scoped conversation items | Rows with metadata + timestamps | FTS5 + BM25 |
| **Archival Memory** | Long-term knowledge, chunked + embedded | F32_BLOB embeddings + FTS5 index | Vector ANN + FTS5 fusion (RRF) |
| **Observational Memory** | Compressed conversation history | Priority-tagged observations with 3-date model | Prefix-cacheable block |
| **DAG Compression** | Hierarchical context summaries | Tree nodes with lossless pointers to originals | Budget-allocated traversal |

The agent interacts with memory through a unified `memory` tool. Large tool results are automatically offloaded to archival memory. Retrieval uses Reciprocal Rank Fusion (RRF) to combine vector similarity and full-text relevance, with recency decay and metadata pre-filtering.

Schema is managed by Goose with 10 versioned migrations.

## CLI Reference

| Command | Description |
|---------|-------------|
| `dragonscale onboard` | Initialize config, workspace, and secret storage |
| `dragonscale agent -m "..."` | One-shot chat |
| `dragonscale agent` | Interactive REPL |
| `dragonscale gateway` | Start message bus gateway |
| `dragonscale status` | Show system status (incl. memory) |
| `dragonscale memory` | Memory system management |
| `dragonscale secret <sub>` | Secret management (init, add, list, delete) |
| `dragonscale daemon <sub>` | Daemon management (start, stop, status) |
| `dragonscale cron list` | List scheduled jobs |
| `dragonscale cron add ...` | Add a scheduled job |
| `dragonscale skills <sub>` | Skill management (install, list, remove) |

## Security

### Workspace Sandbox

File and command access is restricted to the configured workspace by default. The `exec` tool blocks dangerous commands regardless of sandbox setting.

```json
{ "agents": { "defaults": { "restrict_to_workspace": false } } }
```

### Isolated Tool Runtime (ITR)

The SecureBus mediates all tool execution. Tools declare capabilities via the `CapableTool` interface — secrets needed, network endpoints, filesystem paths, shell access level. Tools that don't implement it get zero capabilities.

The pipeline: capability check → secret injection → tool execution → leak scanning → audit log.

See [ADR-001](docs/adr/001-isolated-tool-runtime.md) for the full design including DAG executor convergence, RLM integration, and the FlatBuffers command protocol.

## Development

```bash
make build          # Build for current platform (output: bin/)
make build-all      # Build for current Linux/CGO target (Linux host required, glibc runtime)
make install        # Install to ~/.local/bin + copy skills
make fmt            # go fmt ./...
make deps           # go get -u + go mod tidy
make clean          # Remove build artifacts
```

The Makefile is now a thin compatibility layer.
All task logic is delegated to the `opsctl` Go wrapper binary (`cmd/opsctl`),
so command semantics are preserved while moving orchestration out of shell scripts.

You can run the wrapper directly for faster iteration:

```bash
go run ./cmd/opsctl build
go run ./cmd/opsctl help
```

### Devcontainer (flatc + sqlc ready)

If your host is missing `flatc`/`sqlc`, use the project devcontainer.

```bash
make devcontainer-build
make devcontainer-up
make devcontainer-generate
make devcontainer-verify
```

These targets use `npx @devcontainers/cli`.
- `devcontainer-generate`: runs generation inside the container (`go generate` for FlatBuffers + `sqlc generate`).
- `devcontainer-verify`: verifies generators are idempotent for the current branch state (`make flatc-check sqlc-check`).

### FlatBuffers

FlatBuffers schemas are authoritative and codegen is required:

```bash
go generate ./pkg/itr ./pkg/tools
make flatc-check
```

Schema/codegen mapping:
- `pkg/itr/commands.fbs` → `pkg/itr/itrfb/*`
- `pkg/tools/map_payloads.fbs` → `pkg/tools/mapopsfb/*`

`go:generate` hooks are defined in:
- `pkg/itr/generate_flatbuffers.go`
- `pkg/tools/generate_flatbuffers.go`

Generated files are committed. After schema changes, regenerate and commit updated generated output.

### sqlc

Memory queries are generated by sqlc. After modifying SQL files:

```bash
sqlc generate -f pkg/memory/sqlc/sqlc.yaml
make sqlc-check
```

CI enforces generated code consistency through `make flatc-check` and `make sqlc-check`.
Both checks compare pre/post generation fingerprints (tracked diffs + untracked file hashes) on their target directories, so they work in active (dirty) worktrees while still failing when generated output is stale.

### Migrations

Schema changes go through Goose migrations in `pkg/memory/migrations/`. Migrations run automatically on startup.

### Evaluation

A promptfoo-based evaluation harness lives in `eval/`:

```bash
cd eval && go run ./cmd/eval-runner
```

### Syncing Upstream

```bash
git fetch upstream
git merge upstream/main
```

See `internal/fantasy/VENDORING.md` for syncing the vendored Fantasy SDK.

## Roadmap

See [ROADMAP.md](ROADMAP.md) for the full project roadmap covering context management, skill graphs, agentic retrieval, delegation protocol, and multi-agent coordination.

## Upstream

This project is based on [sipeed/picoclaw](https://github.com/sipeed/picoclaw), originally inspired by [nanobot](https://github.com/HKUDS/nanobot). It keeps the lightweight, single-binary ergonomics while extending agent cognition, security boundaries, and operational capabilities.

## License

MIT — see [LICENSE](LICENSE).
