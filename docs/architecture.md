# OpenLoc Architecture

> Diagrams use [Mermaid](https://mermaid.js.org/). Install your IDE's Mermaid plugin to see rendered visuals.

## System Overview

```mermaid
graph TB
    subgraph Browser
        FE[React + Vite SPA]
    end

    subgraph "AI Clients"
        MCPClient["Claude Code / Desktop<br/>custom agents"]
    end

    subgraph "Go Backend"
        API[Chi Router<br/>REST API]
        Pipeline[Pipeline Service<br/>TM / MT / QE+APE]
        Worker[Asynq Worker<br/>Background Jobs]
    end

    subgraph "Sidecars"
        Parser[Python Parser Sidecar<br/>FastAPI :5200<br/>DOCX / PDF]
        LT[LanguageTool<br/>self-hosted :8010<br/>spell + grammar]
    end

    subgraph "Data Stores"
        PG[(PostgreSQL 17<br/>+ pg_trgm)]
        Redis[(Redis 7<br/>Job Queue)]
        Storage[(SeaweedFS / S3<br/>File Storage)]
    end

    subgraph "External Services"
        LibreT[LibreTranslate<br/>MT Engine]
        LLM[OpenAI / Claude<br/>LLM QE + APE]
        DeepL[DeepL<br/>MT Engine]
        GoogleMT[Google Translate<br/>MT Engine]
        Google[Google OAuth<br/>SSO Provider]
    end

    FE -->|REST + JWT| API
    MCPClient -->|"MCP over Streamable HTTP<br/>/mcp + PAT"| API
    FE -->|OAuth redirect| Google
    Google -->|callback| API
    API --> Pipeline
    API -->|enqueue| Redis
    Redis -->|dequeue| Worker
    Worker --> Pipeline
    Pipeline --> PG
    Pipeline --> Storage
    Pipeline -->|HTTP multipart| Parser
    API -->|POST /v2/check| LT
    Pipeline --> LibreT
    Pipeline --> LLM
    Pipeline --> DeepL
    Pipeline --> GoogleMT
    API --> PG
```

## Pipeline Flow

The core translation pipeline runs per file per target language (= one "job").

```mermaid
flowchart TD
    Upload["File Upload<br/>(parse → segments)"] --> TM

    subgraph "Stage 1: Translate"
        TM["TM Lookup<br/>(exact + fuzzy)"]
        MT["MT Fallback<br/>(LibreTranslate / DeepL)"]
        TM -->|no match| MT
        TM -->|exact match<br/>score = 1.0| Done1[origin: tm_exact]
        TM -->|fuzzy match<br/>score ≥ threshold| Done2[origin: tm_fuzzy]
        MT --> Done3[origin: mt]
    end

    subgraph "Stage 2: QE + APE step (Hybrid and AI workflow steps only)"
        QP["predict<br/>(in batches)"]
        QP -->|approved=true| Auto["Auto-Approved<br/>skip human review"]
        QP -->|approved=false<br/>postedit + ape_approved| APE_OK["APE Fixed<br/>skip human review"]
        QP -->|approved=false<br/>postedit + !ape_approved| APE_Review["APE Suggested<br/>human reviews"]
        QP -->|approved=false<br/>no postedit| Reject["Rejected<br/>human reviews"]
    end

    Done3 --> QP

    subgraph "Stage 3: Human Review"
        CAT["CAT Editor<br/>(segment-by-segment)"]
    end

    Done2 --> CAT
    APE_Review --> CAT
    Reject --> CAT

    subgraph "Stage 4: Completion"
        TMW["TM Write-Back<br/>(human-approved only)"]
    end

    CAT -->|approve / edit| TMW
```

## Entity Relationship Diagram

```mermaid
erDiagram
    tenants ||--o{ users : has
    tenants ||--o{ invites : has
    tenants ||--o{ projects : owns
    tenants ||--o{ mt_engines : configures
    tenants ||--o{ tm_collections : owns
    tenants ||--o{ templates : creates
    tenants ||--o{ tm_profiles : owns
    tenants ||--o{ analysis_weights : configures
    tenants ||--o{ cost_rates : configures

    tenants {
        bigint id PK
        text name
        text slug UK
        text domain
    }

    users {
        bigint id PK
        bigint tenant_id FK
        text email
        text name
        text role "owner|admin|member"
    }

    invites {
        bigint id PK
        bigint tenant_id FK
        text email
        text role
        bigint invited_by FK
        text token UK
        timestamptz accepted_at
        timestamptz expires_at
    }

    projects ||--o{ files : contains
    projects }o--o| templates : "uses (optional)"
    projects }o--o| tm_profiles : "follows (live, via settings_profiles.tm_profile_id)"
    templates }o--o| tm_profiles : follows
    tm_profiles ||--|| settings_profiles : "owns (TM rows + match scoring)"

    files ||--o{ segments : "parsed into"
    files ||--o{ jobs : "generates (per target lang)"

    segments ||--o{ translations : "has (per target lang)"

    segments {
        bigint id PK
        bigint file_id FK
        text source_text
        text segment_key
        int position
        text context
        int word_count
    }

    templates ||--o{ template_tm_assignments : defines
    templates ||--o{ template_mt_assignments : defines

    template_tm_assignments }o--|| tm_collections : references
    template_mt_assignments }o--|| mt_engines : references

    tm_collections ||--o{ translation_memory : stores
    tm_collections ||--o{ tm_imports : tracks

    translations {
        bigint id PK
        bigint segment_id FK
        text target_language
        enum status "untranslated|mt_pending|predict_pending|auto_approved|needs_review|approved"
        enum origin "tm_exact|tm_fuzzy|mt|human|file_import"
        text mt_text
        text ape_text
        text human_text
        text active_text
        enum predict_outcome "approved|ape_approved|ape_needs_review|rejected"
        real tm_match_score
        bigint editing_time_ms "cumulative ms"
        int edit_distance "char-level Levenshtein"
        bigint edited_by FK "last editor"
    }

    analysis_weights {
        bigint id PK
        bigint tenant_id FK
        text band "tm_exact|tm_fuzzy_95|..."
        int weight_pct "0-100"
    }

    cost_rates {
        bigint id PK
        bigint tenant_id FK
        text source_language
        text target_language
        numeric rate_per_word "NUMERIC 10 4"
        text currency "default USD"
    }

    jobs {
        bigint id PK
        bigint file_id FK
        text target_language
        text status "created|queued|in_progress|stopped|completed|failed"
        text current_step
        timestamptz stop_requested_at "pending user stop"
    }

    tm_imports {
        bigint id PK
        bigint tm_collection_id FK
        bigint tenant_id FK
        text filename
        text status "pending|processing|completed|failed"
        int entries_total
        int entries_imported
        text error
    }
```

## Observability

Two destinations with **different sensitivity rules**. Structured logs (slog,
ECS format) carry request, tenant and user identity from `logging.Scope`.
Error-tracking notices go to a third party and must not carry customer content
or credentials — hence the mandatory scrubber, which is a prerequisite for enabling `honeybadger.Handler` at all, not a refinement: the SDK
copies every request header (Cookie, Authorization), the full URL with query
string, and all query params into a notice with nothing gating it.

```mermaid
graph TD
    Req["HTTP request"]
    RID["middleware.RequestID"]
    Rec["middleware.Recoverer<br/>(outermost of the three —<br/>still owns the 500)"]
    ReqObs["requestObservability<br/>creates logging.Scope · logs once<br/>reports status ≥ 500"]
    HBH["honeybadger.Handler<br/>reports panic, then RE-PANICS"]
    Auth["auth.Middleware /<br/>auth.RequireIdentity"]
    AttID["attachIdentity<br/>claims → logging.Scope"]
    H["handler"]

    Scope["logging.Scope<br/>(mutable, context-carried)"]
    Logger["logging logger<br/>slog + ECS handler"]
    ET["errtrack.Capture<br/>ErrorClass + Fingerprint<br/>scrub · rate-limit"]

    Worker["asynq ErrorHandler<br/>terminal failures only"]
    Startup["fatal()<br/>report + FLUSH + exit"]

    ES["Log store<br/>(any ECS-compatible, e.g. Elasticsearch)"]
    HB["Honeybadger<br/>(third party · never content)"]

    FE["Browser (lib/errtrack)<br/>HB JS SDK · placeholder key<br/>scrub + throttle + filter"]
    Proxy["errtrack proxy<br/>POST /api/errtrack/v1/notices/js<br/>re-scrub · env/revision override<br/>attach real key · per-IP limit"]

    Req --> RID --> Rec --> ReqObs --> HBH --> Auth --> AttID --> H
    ReqObs -.creates.-> Scope
    AttID -.fills.-> Scope
    Scope -.read by.-> Logger
    Scope -.read by.-> ET
    ReqObs --> Logger
    ReqObs -->|"5xx, non-panic"| ET
    HBH -->|panic| ET
    Worker --> ET
    Worker --> Logger
    Startup --> ET
    Logger --> ES
    ET --> HB
    FE --> Proxy --> HB
```

Ordering is load-bearing. `honeybadger.Handler` re-panics after reporting, so
`Recoverer` sits outside it and still produces the 500 — and
`requestObservability` sits *inside* `Recoverer`, because from outside it would
see Recoverer's synthesised 500 as an ordinary response and report the same panic
twice. `TestPanicReportedExactlyOnce` fails with 2 notices if that regresses.

`logging.Scope` exists because a top-level middleware can see the final response
status but cannot read auth claims: `auth.Middleware` installs them by *replacing*
the request further down the chain, so the outer middleware still holds the
pre-auth context. A pointer in the context bridges that without `internal/auth`
knowing anything about observability.

5xx notices carry no Go error string, because `httputil.ServerError` discards
it.

## MCP Surface

AI clients reach OpenLoc through `POST /mcp`, mounted **inside** the
authenticated route group so tool handlers can read tenant/user/role from the
same auth claims every REST handler sees. Tools are read, write or destructive;
a write needs a credential with write access, and the tool list a client sees
depends on the caller's role.

```mermaid
graph TD
    Client["MCP client<br/>(Claude Code / Desktop / agent)"]
    Browser["Built-in chat panel"]
    ChatH["chat.Handler<br/>agent loop · tenant's Claude key<br/>SSE over POST /api/chat"]
    InProc["ConnectInProcess<br/>(in-memory transport,<br/>claims ride the connect ctx)"]
    Mw["auth.Middleware<br/>PAT branch → memberships JOIN<br/>(role resolved per request)"]
    RL["Per-token rate limiter"]
    Pick{"role tier?"}
    MgrSrv["manager server<br/>34 tools (reads incl. 3 help-page reads;<br/>writes gated by token capability)"]
    LingSrv["linguist server<br/>(no project/job enumeration;<br/>help pages role-filtered)"]
    Wrap["tool wrapper<br/>role gate · write-capability gate<br/>error sanitising · audit"]
    BE["Backend interface"]
    Direct["directBackend<br/>tenant-scoped queries only"]
    HTTPBE["httpBackend<br/>(future stdio binary)"]
    PG[(PostgreSQL)]
    Audit[(mcp_tool_calls)]

    Client -->|"Bearer eltm_pat_…"| Mw
    Browser -->|"JWT cookie"| Mw
    Mw --> RL --> Pick
    Mw --> ChatH --> InProc --> Pick
    Pick -->|owner/admin/PM| MgrSrv
    Pick -->|linguist| LingSrv
    MgrSrv --> Wrap
    LingSrv --> Wrap
    Wrap --> BE
    BE --> Direct --> PG
    BE -.not yet.-> HTTPBE
    Wrap -.fire-and-forget.-> Audit

    style HTTPBE stroke-dasharray: 5 5
```

The built-in chat is an MCP **client** of this same surface: its
agent loop calls `ConnectInProcess`, which connects the prebuilt per-role
server over an in-memory transport. Identity rides the connect context (the
SDK propagates it into tool-handler contexts — pinned by
`TestInProcessClaimsPropagation`), so chat inherits tenant scoping, role
tiering and auditing without a PAT. Chat sessions have no `PATInfo`, so the
write gate fails closed: chat is read-only by construction, `token_id IS NULL`
marks its audit rows.

Two invariants worth knowing before touching this code:

- **No tool takes a tenant argument.** The PAT fixes the `(user, org)` pair, so a
  model cannot name someone else's org. Tool arguments carry only ids that are
  then verified against the caller's tenant.
- **`directBackend` bypasses the ownership middleware** (`RequireJobOwnership`,
  `RequireFileOwnership`, …) that protects the REST routes. Every query there must
  be tenant-scoped by parameter or structurally via `projects.tenant_id`, and
  every id-taking tool has a cross-tenant test. See the header comment in
  `internal/mcp/backend_direct.go`.

## Frontend Pages

```mermaid
graph LR
    Login["/login"] --> Home
    AcceptInvite["/invite/:token<br/>(public)"] --> Home

    subgraph "Main Navigation"
        Home["/ Projects"]
        Templates["/templates"]
        TMs["/tms"]
        Engines["/engines"]
        Org["/org<br/>Organization"]
        Settings["/settings"]
    end

    Docs["/docs<br/>Help (public, own frame without the nav rail;<br/>with a docs host: 301 to its root)"]
    Docs --> DocsPage["/docs/:section/:page<br/>Markdown from /api/docs<br/>(go:embed, role-filtered; signed out: all-role pages)"]

    Home --> PD["/projects/:id<br/>Project Detail<br/>(Files + Analysis tabs)"]
    PD --> CAT["/projects/:id/jobs/:id/edit<br/>CAT Editor"]
    Templates --> TD["/templates/:id<br/>Template Detail"]
    TMs --> TP["/memories/profiles/:id<br/>TM Profile<br/>(TMs + match scoring)"]
    TMs --> TB["/tms/:id<br/>TM Browser<br/>(Import/Export TMX)"]
```

## Analysis Bands

Standard TMS analysis showing TM leverage, with a QE band for segments the QE+APE step approved.

```mermaid
graph LR
    subgraph "TM Leverage (standard)"
        R[Reps] --> E[100%]
        E --> F95[95-99%]
        F95 --> F85[85-94%]
        F85 --> F75[75-84%]
        F75 --> F50[50-74%]
        F50 --> New[New<br/>no TM match]
    end

    subgraph "QE"
        QE["QE approved<br/>= QE approved<br/>+ APE approved"]
    end

    style QE fill:#7c3aed,color:#fff
    style New fill:#94a3b8,color:#fff
```
