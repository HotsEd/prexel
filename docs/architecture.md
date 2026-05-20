# Arquitetura — visão de nível alto

Doc operacional dos componentes do Prexel e dos fluxos críticos. A spec
detalhada e o rationale de cada decisão ficam no Confluence
([Architecture](https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/655362),
[Stack & Tech Decisions](https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/688130),
[Tech Review — Gaps & Decisões](https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/622598)).

---

## 1. Componentes

O Prexel é **um único binário Go** que assume papéis diferentes conforme o
subcomando: `prexel serve` é o daemon; qualquer outra invocação (`prexel app
list`, `prexel deploy`, etc.) é um cliente HTTP que fala com esse daemon.

```mermaid
flowchart LR
    subgraph operator["Operator host"]
        CLI["prexel CLI<br/>(Cobra subcommands)"]
        Browser["Browser<br/>(Vue 3 SPA)"]
    end

    subgraph server["VPS — prexel serve"]
        Caddy["Caddy<br/>(:80 / :443)<br/>TLS + reverse proxy"]
        API["Chi router<br/>/api/v1/*"]
        SPA["go:embed web/dist<br/>(SPA assets)"]
        Auth["internal/auth<br/>JWT + refresh family + 2FA"]
        Setup["internal/setup<br/>wizard gate"]
        AppSvc["internal/app<br/>CRUD + secrets"]
        DeploySvc["internal/deploy<br/>build → swap → health"]
        DomainSvc["internal/domains<br/>+ dnscheck + sslmonitor"]
        ServerSvc["internal/server<br/>local + remote provider"]
        EventBus["internal/eventbus<br/>SSE fan-out"]
        Cleanup["internal/appcleanup<br/>async GC"]
        DB[("SQLite<br/>modernc.org/sqlite")]
    end

    subgraph external["External"]
        Docker["Docker daemon<br/>(local socket or ssh://)"]
        CaddyAdmin["Caddy Admin API<br/>localhost:2019"]
        GitHub["GitHub<br/>(App / SSH / PAT)"]
        LE["Let's Encrypt"]
    end

    CLI -->|HTTPS + JWT| API
    Browser -->|HTTPS + JWT| API
    Browser -->|HTML| Caddy
    Caddy --> SPA
    Caddy --> API

    API --> Auth
    API --> Setup
    API --> AppSvc
    API --> DeploySvc
    API --> DomainSvc
    API --> ServerSvc
    API --> EventBus

    AppSvc --> DB
    Auth --> DB
    DeploySvc --> DB
    DomainSvc --> DB
    ServerSvc --> DB
    Setup --> DB
    Cleanup --> DB

    DeploySvc -->|build / run| Docker
    DeploySvc -->|route upsert| CaddyAdmin
    DeploySvc -->|clone| GitHub
    DomainSvc -->|tls enable| CaddyAdmin
    Caddy -->|ACME| LE
    ServerSvc -->|inspect| Docker
    Cleanup -->|rm container/image| Docker
```

Resumo:

- **CLI e Web UI** são duas frentes para a mesma API REST. Nenhum dos dois
  acessa banco, Docker ou Caddy diretamente — a regra é estrita (ver
  `CLAUDE.MD §"CLI nunca duplica lógica de backend"`).
- **Caddy** termina TLS no `:443` e roteia: requests para o host da
  instância vão para o API server (Chi) ou pra SPA embedada via `go:embed
  web/dist`; requests para hosts de apps gerenciados vão pros containers na
  bridge `prexel-net`.
- **SQLite** (modernc, pure-Go) é o único banco. Migrations via
  `golang-migrate` aplicadas no boot.
- **Docker daemon** acessado via socket local ou `ssh://` (servidor remoto).
  Cada `internal/server` Provider abstrai o transporte.
- **Eventbus** in-memory + ring buffer alimenta SSE (`/events`, logs de
  deploy) com replay via `Last-Event-ID`.

---

## 2. Fluxo de deploy

```mermaid
sequenceDiagram
    autonumber
    participant U as Operator
    participant C as CLI / SPA
    participant A as API (/apps/:id/deploy)
    participant D as deploy.Engine
    participant G as gitsrc (clone)
    participant B as build (docker build / pull)
    participant Dk as Docker daemon
    participant Cy as Caddy admin
    participant H as healthcheck loop
    participant Bus as eventbus

    U->>C: prexel deploy my-app --watch
    C->>A: POST /apps/:id/deploy (Bearer JWT)
    A->>D: Enqueue(deployment)
    A-->>C: 202 + deployment_id
    C->>Bus: SSE /events?last-event-id=...
    D-->>Bus: deploy.started
    D->>G: Clone (only when build_type=dockerfile)
    G-->>D: workspace path
    D->>B: docker build / pull
    B-->>Bus: build logs (stdout/stderr)
    B-->>D: image ref
    D->>Dk: docker run candidate (label prexel.deployment_id=new)
    Dk-->>D: container_id
    D->>H: poll TCP/HTTP healthcheck
    alt Health OK
        D->>Cy: PUT route upstream=new
        D->>Dk: stop + rm old container
        D-->>Bus: deploy.success
    else Health fail
        D->>Dk: stop + rm new container
        D-->>Bus: deploy.failed (auto-rollback)
    end
```

Pontos chave:

- **Zero downtime**: a swap só acontece após o healthcheck do candidato passar.
  O container antigo continua servindo até a rota do Caddy ser atualizada.
- **Auto-rollback** dispara quando o healthcheck do candidato falha — o
  container antigo nunca é tocado nesse caminho.
- **Logs em tempo real**: cada linha de build + cada transição de estado vão
  pra `eventbus` e são consumidas por SSE no UI/CLI (`--watch`).
- **Retenção de imagens**: `internal/appcleanup` mantém as últimas N imagens
  por app (5 por padrão); deploys mais antigos são GCed pro Docker prune.

---

## 3. Fluxo de autenticação

```mermaid
sequenceDiagram
    autonumber
    participant U as Browser / CLI
    participant API as /auth
    participant Auth as internal/auth
    participant DB as SQLite

    U->>API: POST /auth/login {email, password}
    API->>Auth: VerifyPassword + (se 2FA) BeginChallenge
    alt sem 2FA
        Auth->>DB: insert refresh_token (family_id=new)
        Auth-->>API: access_token (15min) + Set-Cookie refresh_token
        API-->>U: 200 + access_token JSON
    else com 2FA
        Auth-->>API: requires_2fa, challenge_id
        API-->>U: 200 {requires_2fa, challenge_id}
        U->>API: POST /auth/2fa/challenge {challenge_id, code}
        API->>Auth: VerifyTOTP / VerifyRecoveryCode
        Auth->>DB: insert refresh_token (family_id=new)
        Auth-->>API: access_token + Set-Cookie refresh_token
        API-->>U: 200
    end

    Note over U,API: Access token expira em ~15min

    U->>API: POST /auth/refresh (cookie)
    API->>Auth: RotateRefresh(old_id)
    alt token válido e não usado
        Auth->>DB: mark old used, insert new (same family_id)
        Auth-->>API: new access_token + Set-Cookie refresh_token (novo)
        API-->>U: 200
    else token já marcado used (REUSE)
        Auth->>DB: invalidate family_id (todos os refresh)
        Auth-->>API: 401 + log "refresh token reuse detected"
        API-->>U: 401
        Note over U: forçado a login completo
    end
```

Por que famílias?

- Refresh tokens são rotacionados a cada uso. O ID antigo fica no banco
  marcado `used_at=...`.
- Se o ID antigo for usado de novo (típico de roubo de cookie), a família
  inteira é invalidada — o atacante e a vítima ambos perdem acesso, e o
  evento entra no audit log. Mais barato que detectar "qual dos dois é o
  atacante" e mais seguro que confiar.

---

## 4. Setup wizard

State machine do `prexel serve` na 1ª boot até o admin existir:

```mermaid
stateDiagram-v2
    [*] --> Booting

    Booting --> NoSetup: SetupComplete = false
    Booting --> Operational: SetupComplete = true

    NoSetup --> Welcome: GET /setup/status
    Welcome --> CreateAdmin: POST /setup/admin {email, password}
    CreateAdmin --> SaveInstance: 201
    SaveInstance --> CreateServer: POST /setup/instance {url, tls_mode}
    CreateServer --> Complete: POST /setup/server {local|remote, ssh_key?}
    Complete --> Operational: POST /setup/complete (atomic)

    Operational --> [*]

    note right of NoSetup
        SetupGate middleware:
        — rotas /setup retornam 200
        — qualquer outra rota retorna 404
        impede que login/CRUD funcionem
        antes da config inicial.
    end note

    note right of Operational
        SetupGate flip:
        — rotas /setup retornam 404
        — restante da API liberada
          (sujeito a Auth + RBAC).
    end note
```

`MarkComplete` é idempotente sob concorrência (testado em
`internal/setup/setup_test.go`) — duas chamadas paralelas não criam dois
admins.

---

## 5. Decisões de arquitetura (resumo)

| Decisão                          | Por quê                                                                                                                               |
|----------------------------------|---------------------------------------------------------------------------------------------------------------------------------------|
| **Single binary**                | `caddy`/`consul`/`nomad`-style. Instalação trivial (`curl \| sh`), upgrades atômicos, sem orquestração lateral.                       |
| **SQLite (modernc, pure-Go)**    | Zero ops. Não precisa de Postgres pra rodar 1 instância. Backup = `tar /var/lib/prexel`. Pure-Go = sem CGO no build estático.         |
| **Caddy embutido**               | TLS automático com Let's Encrypt, admin API JSON-first (sem reload de config), suporta on-demand TLS para domínios de tenants.        |
| **Vue 3 + embed**                | SPA empacotada no binário via `go:embed web/dist`; deploy do UI = deploy do servidor. History fallback no router serve `index.html`.  |
| **Chi router**                   | Middleware nativo + sub-routers; cabeçalho idiomático Go (em vez de gin, etc.).                                                       |
| **JWT HS256 (não RS)**           | 1 chave, 1 audience — não estamos federando. HS é menos bug-prone e mais rápido.                                                      |
| **Refresh por família**          | Detecção de reuse com baixo custo de implementação. Ver §3.                                                                           |
| **AES-256-GCM com HKDF**         | Cripto padrão moderna autenticada. HKDF deriva sub-keys por purpose (secret vs. SSH key vs. TOTP).                                    |
| **Eventbus in-memory + ring buffer** | Para SSE de logs e progresso. Não precisa de Redis pra v0.1; replay via `Last-Event-ID` cobre reconexões.                          |
| **Docker via socket / `ssh://`** | Single abstraction para local + remote — `internal/server.Provider`. Sem agent custom em cada VPS.                                    |
| **Sem `prexelctl`**              | Confunde packaging. `prexel serve` + `prexel <verb>` são o mesmo binário (ver `CLAUDE.MD`).                                           |

A discussão longa de cada item está em
[Stack & Tech Decisions](https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/688130)
e nas [Análise Coolify vs Prexel](https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/1212419)
no Confluence.

---

## 6. Layout de código (recap)

```
main.go                       # ~30 linhas; injeta embed.FS em internal/cli.Execute
internal/cli/command/         # Cobra tree (serve.go + clientes)
internal/cli/client/          # cliente HTTP (TOFU, cookie store, JWT)
internal/api/                 # router.go + handler/* + apimiddleware/*
internal/{auth,setup,app,deploy,domains,server,gitsrc,secret,...}/
                              # serviços (acessam DB / Docker / Caddy)
internal/crypto/              # AES-GCM + HKDF
internal/eventbus/            # SSE fan-out + ring buffer
web/                          # Vue 3, build vai pra web/dist (embedado)
migrations/                   # golang-migrate (up/down pares)
```

Princípio de dependência: `internal/cli/*` só pode importar
`internal/cli/client`, `internal/cli/tableutil`, `internal/cli/config` — nunca
um service. Quebrar isso quebra a paridade CLI ↔ UI (CLI deixaria de testar a
mesma rota que a UI usa).
