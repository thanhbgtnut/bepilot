<div align="center">
  <div>BePilot</div>
  <div>
    <a href="https://opensource.org/licenses/Apache-2.0">
      <img src="https://img.shields.io/badge/License-Apache2.0-brightgreen.svg?style=flat" alt="License: Apache 2.0">
    </a>
    <a href="https://github.com/thanhbgtnut/bepilot">
      <img src="https://img.shields.io/github/stars/thanhbgtnut/bepilot.svg?style=flat&logo=github&label=Stars" alt="Stars">
    </a>
    </a>
    <a href="https://github.com/thanhbgtnut/bepilot/releases">
      <img src="https://img.shields.io/github/v/release/thanhbgtnut/bepilot?style=flat&label=Latest%20Release&color=6D28D9" alt="Latest Release">
    </a>
    <a href="https://deepwiki.com/thanhbgtnut/bepilot"><img src="https://deepwiki.com/badge.svg" alt="Ask DeepWiki"></a>
    <a href='https://codespaces.new/thanhbgtnut/bepilot'>
      <img src='https://github.com/codespaces/badge.svg' alt='Open in Github Codespaces' style='max-width: 100%;' height="20">
    </a>
  </div>
  <div>
    The <strong>first complete</strong> connectivity solution for Agentic AI.
  </div>
</div>


A streaming AI agent backend built with **[Eino](https://github.com/cloudwego/eino)**
(agent orchestration), **[Hertz](https://github.com/cloudwego/hertz)** (HTTP), and
**PostgreSQL + pgvector** (persistence).

The HTTP surface mirrors the **Anthropic Messages API** so existing Claude
clients work with minimal changes, and adds session-management endpoints so you
can list a user's chats and replay full transcripts.

## What makes the agent good

| Capability | How it works |
|---|---|
| **Dynamic system prompt** | Rebuilt every turn from ordered sections (`internal/agent/prompt`): identity, a live `<environment>` block (date/time, model, session, tools), rolling conversation summary, tool-use guidance, a **retrieved skill index**, response style, and per-session instructions. |
| **Exact math & valid JSON** | The `calculate` tool evaluates arithmetic with exact big-rational math (many expressions per call, no float error), and `json_validate` reports the line/column and a fix hint for invalid JSON. An always-on `<accuracy>` prompt section tells the model to compute with `calculate` instead of in its head and to never write expressions like `a + b` inside JSON values. As a server-side guarantee independent of the model, JSON in the answer (a ```` ```json ```` fence or a bare object/array) is held until complete and any arithmetic left in a value position is evaluated exactly before it is streamed or stored; prose and other code blocks are untouched, and each rewrite is logged as a warning. |
| **Automatic skill discovery** | Every turn, the current conversation (last user message + rolling summary) is embedded and matched against skill descriptions via pgvector. Relevant skills are named in the prompt and loadable with the `load_skill` tool — **the user never has to mention a skill by name**. A skill's `allowed_tools` are bound automatically when it is retrieved. |
| **Tool search** | Built-in tools (`current_time`, `calculate`, `json_validate`, `http_fetch`, `web_search`, `load_skill`, `tool_search`) are always bound. External tools (MCP servers etc.) are *deferred*: only their names appear in the prompt, and the model loads the ones it needs with the built-in `tool_search` tool (`select:<name>` or keywords) before calling them — so the context stays small however many tools are attached. With no external tools attached, nothing changes and `tool_search` is not offered. A skill's `allowed_tools` are loaded automatically. |
| **MCP servers, hot-attached** | Model Context Protocol servers (stdio / SSE / streamable HTTP) declared in a config file **or** added at runtime via `POST /v1/mcp/servers`. Their tools register into the shared tool registry as `mcp__<server>__<tool>` as *deferred* tools (discovered via `tool_search`) and are picked up on the next message — **no restart**. See [MCP servers](#mcp-servers). |
| **Never blocks on a new message** | A session runs one turn at a time, but a message sent while a turn is running does not wait for it. It is stored at once and handed to the running turn, which reads it before its next model call (`POST /v1/messages` answers `202 {"type":"steered"}`; on AG-UI the request returns an empty `RUN_STARTED`/`RUN_FINISHED` and the reply appears on the stream already open). A short stop message (`stop`, `dừng`, `hủy`) interrupts the turn instead: it keeps what it produced and ends with `stop_reason: "interrupted"`. A message that arrives too late for the model to read is answered by a follow-up turn, so none is lost. Writers to one session are serialised in the database, so `seq` never collides. |
| **Long answers finish** | A reply cut off by the output limit (`finish_reason` `length`/`max_tokens`) is continued automatically — up to 4 times per model call, streamed as one uninterrupted reply — so a long document is never left ending mid-sentence or mid-JSON. A cut-off tool call is not continued. |
| **Budget-aware turns** | The prompt states the turn's model-call budget (`agent.max_steps`); three calls before the end the model is warned, and the last call has no tools and must answer. A task that runs long therefore ends with a complete answer, not an error or a silent cut. |
| **Comparable runs** | Every turn stores its settings and behaviour in `agent_runs.detail` (stop reason, `max_tokens`, temperature, tool calls by name, skills, steered messages, interruption). `llm.temperature` sets a default for requests that give none, so a low value makes the same request repeatable. |
| **True streaming** | The Eino ReAct loop runs in streaming mode; a callback handler turns every model delta and tool call/result into an internal event stream that is mapped to Anthropic SSE frames (`message_start` → `content_block_*` → `message_delta` → `message_stop`), with `tool_execution_start` / `tool_execution_stop` extension events for progress. |
| **Context management** | History is loaded from Postgres, converted to a well-formed transcript, and trimmed to a token budget; a background job refreshes a rolling summary every N turns. |
| **Multi-provider, runtime-selectable** | `claude`, `openai`-compatible, `ark` (Volcengine), and a deterministic `fake` provider for offline dev/tests. Pick per request (`"provider"`) or via config default. |
| **Crash-safe persistence** | The user message is stored on receipt; the assistant message (including partial output on error or client disconnect) is stored with a detached context so a dropped connection never loses a turn. |
| **Observability** | One `agent_runs` row per turn (provider, model, steps, tokens, latency, error); structured `slog` logs with request ids. |

## Project layout

```
cmd/
  server/        HTTP server (also: -migrate-only)
  seed/          create a user + print an API key
  skills-sync/   sync skills/ into Postgres + embeddings
internal/
  config/        YAML + ${ENV} config loading
  domain/        persistence-agnostic entities
  store/         pgx pool, embedded goose migrations, repositories
  retrieval/     Embedder interface + hash (offline) and OpenAI implementations
  llm/           Provider abstraction + registry; fakeprovider/
  skills/        SKILL.md parsing, discovery, sync, retrieval, load
  tools/         built-in eino tools + concurrency-safe registry
  mcp/           MCP client manager: dial servers, import tools, file watcher
  agent/         the turn executor
    prompt/      dynamic system-prompt sections
    events/      internal event model + Anthropic SSE mapper
  server/        Hertz engine, middleware, SSE writer
  api/           HTTP handlers + Anthropic-shaped DTOs
docs/            OpenAPI spec generated by swaggo/swag + Swagger UI, embedded — see docs/README.md
skills/          skill documents (source of truth)
deploy/          docker-compose (Postgres+pgvector), Dockerfile
configs/         config.yaml, mcp.yaml (MCP server list)
```

## Quick start (no API keys needed)

Requires Go 1.26+ and Docker.

```bash
cp .env.example .env                 # defaults to the offline 'fake' provider
docker compose -f deploy/docker-compose.yml up -d   # Postgres+pgvector on :5433
make migrate                         # apply schema
make skills-sync                     # load skills/ into Postgres + embeddings
make seed                            # prints an API key — copy it
make run                             # server on :8080
```

`make dev` does `up` + `migrate` + `run` in one step.

### Talk to it

```bash
export KEY=sk-bepilot-...            # from `make seed`

# Non-streaming. Note: the question never mentions a skill, but the agent
# retrieves the "pdf-forms" skill and calls load_skill on its own.
curl -s localhost:8080/v1/messages \
  -H "x-api-key: $KEY" -H 'content-type: application/json' \
  -d '{
    "model": "bepilot-fake-1",
    "max_tokens": 512,
    "messages": [{"role":"user","content":"help me fill out a fillable PDF form with my contact details"}]
  }' | jq

# Streaming (Anthropic SSE frames)
curl -sN localhost:8080/v1/messages \
  -H "x-api-key: $KEY" -H 'content-type: application/json' \
  -d '{"model":"bepilot-fake-1","max_tokens":512,"stream":true,
       "messages":[{"role":"user","content":"clean up this messy customer csv"}]}'

# Continue an existing conversation
curl -s localhost:8080/v1/messages -H "x-api-key: $KEY" -H 'content-type: application/json' \
  -d '{"model":"bepilot-fake-1","max_tokens":256,
       "metadata":{"session_id":"<SESSION_ID>"},
       "messages":[{"role":"user","content":"and now flatten it"}]}' | jq

# Sessions for the authenticated user
curl -s "localhost:8080/v1/sessions?limit=20" -H "x-api-key: $KEY" | jq

# Full transcript (Anthropic content-array shape, includes tool_use/tool_result)
curl -s "localhost:8080/v1/sessions/<SESSION_ID>" -H "x-api-key: $KEY" | jq
```

### Interactive API docs

Swagger UI is served at **`http://localhost:8080/docs`** (redirects to
`/swagger/index.html`); the raw spec is at `http://localhost:8080/openapi.yaml`.
Both are public and fully offline (UI assets are embedded).

The spec is **generated from handler annotations** with `swaggo/swag`. After
adding or changing an endpoint, run `make swag` and commit `docs/`. See
**[`docs/adding-endpoints.md`](docs/adding-endpoints.md)**; `make test` fails if a
route has no annotation.

### Use real Claude

In `.env`:

```bash
BEPILOT_DEFAULT_PROVIDER=claude
BEPILOT_DEFAULT_MODEL=claude-sonnet-5
ANTHROPIC_API_KEY=sk-ant-...
BEPILOT_EMBEDDING_KIND=openai     # optional: real semantic skill retrieval
OPENAI_API_KEY=sk-...
```

Restart, `make skills-sync` (re-embeds with the new embedder), `make run`.
Per-request override: add `"provider": "claude"` to the body.

### Use a local OpenAI-compatible server (LM Studio, llama.cpp, vLLM, Ollama)

No API key needed. In `.env`:

```bash
BEPILOT_DEFAULT_PROVIDER=openai
BEPILOT_DEFAULT_MODEL=<model id from your server>
OPENAI_BASE_URL=http://127.0.0.1:1234/v1     # LM Studio's default
```

`OPENAI_BASE_URL` may be the API base or a full endpoint URL
(`.../v1/chat/completions`) — the suffix is trimmed automatically. The same
setting powers `BEPILOT_EMBEDDING_KIND=openai` if your server exposes
`/v1/embeddings` (keep `embedding.dim` = the model's dimension; the pgvector
column is 1536 by default).

## API

### `POST /v1/messages`  (Anthropic-compatible)

Body: `model`, `max_tokens`, `messages[]` (content: string or block array),
`system` (string or block array), `temperature`, `stream`, plus extensions
`provider` and `metadata.session_id`. Auth: `x-api-key` header (or
`Authorization: Bearer`).

- Buffered response: `{ id, type:"message", role:"assistant", content:[…],
  stop_reason, usage, session }`.
- Streaming (`"stream": true` or `Accept: text/event-stream`): `message_start`,
  `content_block_start` / `content_block_delta` (`text_delta`,
  `input_json_delta`, `thinking_delta`) / `content_block_stop`, `message_delta`,
  `message_stop`, `ping`, `error`, plus `tool_execution_start` /
  `tool_execution_stop`.

If `metadata.session_id` is omitted a new session is created (titled from the
first message). If present, the session must belong to the caller.

### `POST /v1/ag-ui/run`  (AG-UI protocol)

For clients built on the [AG-UI protocol](https://docs.ag-ui.com) (CopilotKit's
`HttpAgent`, etc.). Same agent, same auth — only the wire format differs.

Body: an AG-UI `RunAgentInput` (`threadId`, `runId`, `messages[]`, and the
accepted-but-ignored `state` / `tools` / `context`). Only the last `user`
message drives the turn; history is loaded server-side from the thread.

Response is always an AG-UI SSE stream: `RUN_STARTED` →
`TEXT_MESSAGE_START` / `TEXT_MESSAGE_CONTENT` / `TEXT_MESSAGE_END`,
`TOOL_CALL_START` / `TOOL_CALL_ARGS` / `TOOL_CALL_END` / `TOOL_CALL_RESULT` →
`RUN_FINISHED` (or `RUN_ERROR`). Each frame is a `data:` line whose JSON has a
`type` field. Thinking/reasoning deltas are not forwarded on this surface.

`threadId` **is** the bepilot session id: pass an existing one to continue a
conversation, or leave it empty to start fresh — the new id comes back in
`RUN_STARTED.threadId`, so persist it for the next turn. A `threadId` owned by
another user is rejected; an unknown one just starts a new session.

The internal event stream is mapped to both formats by
`internal/agent/events/{anthropic,agui}.go`; `/v1/messages` is untouched.

### Sessions

| Method | Path | Purpose |
|---|---|---|
| `POST`   | `/v1/sessions` | create a session (optional `title`, `provider`, `model`, `system`, `metadata`) |
| `GET`    | `/v1/sessions?limit=&cursor=` | **list the caller's sessions**, newest first, keyset-paginated |
| `GET`    | `/v1/sessions/{id}` | session + reconstructed transcript |
| `GET`    | `/v1/sessions/{id}/messages?after_seq=&limit=` | messages only |
| `PATCH`  | `/v1/sessions/{id}` | update `title` / `metadata` |
| `DELETE` | `/v1/sessions/{id}` | soft delete |

### Skills, docs & health

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/skills/sync` | rescan `skills/`, upsert + re-embed changed, delete removed |
| `GET`  | `/v1/skills` | list skills and their status |
| `GET`  | `/docs`, `/swagger/*`, `/openapi.yaml` | Swagger UI + generated spec (public) |
| `GET`  | `/healthz`, `/readyz` | liveness / readiness |

### MCP

| Method | Path | Purpose |
|---|---|---|
| `GET`    | `/v1/mcp/servers` | list attached servers (file- and API-registered) with their imported tools |
| `POST`   | `/v1/mcp/servers` | attach or replace a server; dials synchronously, persists the spec |
| `DELETE` | `/v1/mcp/servers/{name}` | detach an API-registered server and forget its spec |

Returns `503` when MCP is disabled, `409` when the name is owned by the config
file, `502` when the server can't be dialed. See [MCP servers](#mcp-servers).

## Skills

A skill is a directory under `skills/` containing `SKILL.md` with YAML
frontmatter:

```markdown
---
name: PDF Forms
description: Inspect, fill and flatten AcroForm PDF forms. Use whenever the user
  wants to complete a fillable PDF, extract form fields, merge data into a
  template, or flatten a completed form.
allowed_tools:
  - http_fetch
---

# PDF Forms
...step-by-step instructions and notes...
```

`description` drives retrieval — write it as "what this is + when to use it".
Files are the source of truth; `make skills-sync` (or `POST /v1/skills/sync`)
reconciles Postgres and (re)computes embeddings by checksum.

## MCP servers

bepilot can attach external **[Model Context Protocol](https://modelcontextprotocol.io)**
servers and expose their tools to the agent. Tools import under a namespaced
name — `mcp__<server>__<tool>` — so servers never collide. They are *deferred*:
the prompt lists only their names, and the model loads a tool's schema with the
built-in `tool_search` (`select:<name>` or keywords, `+word` to require a name
match) before calling it. A tool loaded in one turn stays loaded for the rest of
the conversation. MCP is optional — with no servers attached, `tool_search` is
simply not offered and only the built-in tools are bound.

**The key property: adding or removing a server never needs a restart.** The
agent resolves tools from the shared registry on every message, so a
newly-attached server's tools are usable on the very next turn. A tool is only
invoked over its transport when the model actually calls it; listing tools costs
nothing at request time.

### Enable it

Off by default. In `.env`:

```bash
BEPILOT_MCP_ENABLED=true
```

Relevant `configs/config.yaml` block (all optional, defaults shown):

```yaml
mcp:
  enabled: ${BEPILOT_MCP_ENABLED}   # false → subsystem off, API returns 503
  file: configs/mcp.yaml            # static server list; blank → API only
  reload_interval: 10s              # how often the file is re-read; 0 → once
  init_timeout: 20s                 # per-server connect + handshake budget
  tool_prefix: "mcp__"             # tools import as <prefix><server>__<tool>
```

### Two sources, both hot

**1. Config file — `configs/mcp.yaml`.** Edit while the server runs; changes are
reconciled within `reload_interval` (new entry → dialed, removed/`disabled` →
detached, changed → redialed). `${ENV}` is expanded like the main config.

```yaml
servers:
  # stdio: bepilot spawns a child process and talks over stdin/stdout
  - name: github
    transport: stdio
    command: docker
    args: ["run", "-i", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "ghcr.io/github/github-mcp-server"]
    env:
      GITHUB_PERSONAL_ACCESS_TOKEN: ${GITHUB_TOKEN}

  # streamable_http: remote server (recommended for production)
  - name: search
    transport: streamable_http
    url: https://mcp.example.com/mcp
    headers:
      Authorization: "Bearer ${SEARCH_MCP_TOKEN}"
    tool_allowlist: ["web_search", "fetch_url"]   # import only these tools

  # sse: older SSE-style server
  - name: internal
    transport: sse
    url: http://127.0.0.1:9000/sse

  - name: experimental
    transport: stdio
    command: ./bin/my-mcp-server
    disabled: true           # keep the entry, don't attach
```

**2. API — `/v1/mcp/servers`.** Servers added this way are stored in Postgres
(`mcp_servers` table, migration `0003`) and reattached automatically on the next
start. Do **not** also list them in `configs/mcp.yaml` — the file entry is
ignored when the name is API-owned.

```bash
# attach (re-POST the same name to replace + redial)
curl -s localhost:8080/v1/mcp/servers -H "x-api-key: $KEY" -H 'content-type: application/json' \
  -d '{
    "name": "github",
    "transport": "stdio",
    "command": "docker",
    "args": ["run","-i","--rm","-e","GITHUB_PERSONAL_ACCESS_TOKEN","ghcr.io/github/github-mcp-server"],
    "env": {"GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_..."}
  }' | jq

# list what's attached, and the tools each server contributed
curl -s localhost:8080/v1/mcp/servers -H "x-api-key: $KEY" | jq

# detach + forget
curl -s -X DELETE localhost:8080/v1/mcp/servers/github -H "x-api-key: $KEY" | jq
```

`POST` dials the server before returning: `502` means the server could not be
reached (nothing is persisted), `409` means the name belongs to the config file.

### How it works

`internal/mcp` holds a `Manager` of live client connections (SDK:
`mark3labs/mcp-go`, adapter: `eino-ext/components/tool/mcp`). On attach it dials,
runs the MCP `Initialize` handshake, calls `tools/list`, wraps each tool under
its prefixed name and registers it into `internal/tools.Registry` (which is
concurrency-safe). A stdio child process is tied to a background context, so it
outlives individual requests and is only killed on detach or shutdown. The file
watcher polls the file's mtime and also retries any declared-but-unattached
server each interval. Database reads happen only at startup (bootstrap) and on
API writes — never on the request path.

## Configuration

`configs/config.yaml` with `${ENV}` expansion. Well-known env overrides:
`DATABASE_URL`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`,
`BEPILOT_DEFAULT_PROVIDER`, `BEPILOT_HTTP_ADDR`. See `.env.example`.
`configs/mcp.yaml` is a separate, hot-reloaded file for the MCP server list
(see [MCP servers](#mcp-servers)).

The pgvector column dimension is fixed at **1536** in
`internal/store/migrations/0002_skills.sql`; if you switch to an embedder with a
different dimension, edit that migration and reset the DB.

## Tests

```bash
make test                                  # network-free unit tests
TEST_DATABASE_URL=postgres://bepilot:bepilot@localhost:5433/bepilot?sslmode=disable \
  go test ./...                             # + full HTTP integration tests (fake provider)
```

Integration tests spin up the real router against a real Postgres and assert the
end-to-end streaming path, skill auto-discovery, session listing, and transcript
reconstruction.

## Notes / not included

- No rate limiting, org/multi-tenant model, or prompt caching yet.
- `http_fetch` refuses loopback/private hosts; `web_search` is a stub until a
  search backend is configured. There is no arbitrary code execution tool (`calculate` is a sandboxed arithmetic parser, not `eval`).
- MCP tool calls are **not** sandboxed and inherit the server process's
  privileges — only attach servers you trust. Single-instance only: an
  API-registered server attaches to the process that received the call; other
  replicas pick it up on their next restart (bootstrap from Postgres).
- The `fake` provider is deterministic and for development/testing only.
