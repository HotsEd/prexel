# CLAUDE.md

## Documentação do Projeto

Toda a documentação do Prexel está no Confluence. **Antes de qualquer tarefa, consulte as páginas relevantes.**

**Espaço:** https://hotsed.atlassian.net/wiki/spaces/Prexel/overview

## Páginas principais

| Página | URL |
|--------|-----|
| Home | https://hotsed.atlassian.net/wiki/spaces/Prexel/overview |
| Architecture | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/655362 |
| Data Models | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/884738 |
| API Reference | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/851969 |
| Security & Communication | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/1310722 |
| Setup Flow | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/1179650 |
| Stack & Tech Decisions | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/688130 |
| UI Spec | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/1376258 |
| MVP Spec (v0.1) | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/851969 |
| Tech Review — Gaps & Decisões | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/622598 |
| Análise Coolify vs Prexel | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/1212419 |

## Specs do MVP (subpáginas)

| Spec | URL |
|------|-----|
| Setup & Auth | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/1310722 |
| Servers | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/1343490 |
| Domains | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/1376258 |
| Apps & Deploy | https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/1409026 |

## Regras de implementação

### Spec antes de código
Antes de implementar qualquer feature, leia a spec correspondente no Confluence.
Em caso de dúvida sobre decisões de arquitetura, consulte o Tech Review e a Análise do Coolify.

### Paridade CLI ↔ UI (não-negociável)
**Toda nova rota da API REST tem que ganhar um subcomando no CLI no mesmo PR/sessão.**
O Prexel se diferencia exatamente por oferecer paridade CLI ↔ UI — se uma feature
só existe no painel, ela não está pronta. Heurística:

- Endpoint `GET /apps` → `prexel app list`
- Endpoint `POST /apps/:id/deploy` → `prexel deploy <app>`
- Endpoint `PATCH /domains/:id` → `prexel domain edit <id>`
- Endpoint SSE `/events` → `prexel events --watch`

Exceções aceitas (raras): wizards interativos complexos (setup multi-step) e
fluxos OAuth de browser (GitHub App install). Mesmo nesses casos, deve haver
um caminho CLI alternativo (ex.: `setup` em TUI Bubbletea).

### CLI nunca duplica lógica de backend
O CLI é estritamente um **client HTTP** que chama `prexel serve`. Camadas
permitidas: parse de flags (Cobra), HTTP via `internal/cli/client/http.go`,
render via `internal/cli/tableutil`/`render`. **Zero acesso direto a
DB/Docker/SSH/Caddy/Crypto** — toda essa lógica fica em `internal/*` (não-cli)
e é exercitada via endpoints REST.

Exceção única: subcomandos sob `prexel admin *` que rodam OFFLINE no servidor
quando o daemon está parado (`reset-password`, `restore`). Esses operam direto
no SQLite local porque o caso de uso é justamente "API inacessível".

### CLI nunca consome a web UI do Prexel (não-negociável)
O CLI **NÃO** pode abrir, redirecionar para, embedar ou de qualquer forma
depender da web UI do Prexel para concluir uma operação. Isso inclui:

- ❌ Imprimir/abrir URL do tipo `<instance>/git`, `<instance>/apps/new`, etc.
- ❌ "Faça pelo painel" como fallback ao não conseguir resolver algo no CLI.
- ❌ Webhooks/callbacks que dependam da SPA estar aberta para fechar o loop.

Se um fluxo precisa de browser (ex.: OAuth com GitHub App), o CLI **sobe um
HTTP server local efêmero** (`localhost:<random port>`) e abre o browser
direto no provedor externo (GitHub, etc.). Os callbacks voltam para o
localhost, o CLI processa e fala com a API REST do Prexel pelas mesmas
rotas que a UI usaria. O máximo de UI permitido é uma página HTML
minimalista servida pelo próprio CLI no localhost com "Pode fechar essa
janela".

Razão: paridade real significa "CI/cron/script consegue 100% do CLI sem
um humano abrir browser na UI". Reaproveitar a UI é dívida que vaza para
fora do CLI e quebra automação.

## Features fora do escopo v0.1 (paridade CLI parcial)

Os seguintes endpoints existem na API REST mas seus subcomandos CLI virão em
**v0.2+**. Tratar como dívida planejada — *não* contam contra a regra de
paridade enquanto estiverem listados aqui. Antes de mover qualquer item para
v0.2, escreva o subcomando no mesmo PR/sessão da feature.

- **2FA TOTP** — habilitar/confirmar/desabilitar, regenerar recovery codes,
  e (especialmente) `prexel login` tratando o caso `requires_2fa` em vez de
  forçar o usuário a desabilitar 2FA via web pra logar pelo CLI.
- **Instance management** — `prexel instance show/edit` (URL pública, TLS
  mode, branding mínimo). Hoje só via setup wizard + edição manual no banco.
- **Team & Role management** — `prexel team {list,create,invite,members}` e
  `prexel role {list,grant,revoke}`. RBAC funciona no backend; UI tem CRUD;
  CLI não.
- **Backup / Restore** — `prexel admin backup` e `prexel admin restore` já
  existem como caminho offline; falta `prexel backup {list,download,schedule}`
  pra operar com o daemon vivo.
- **Profile** — `prexel profile {show,update,change-password}`. Só web.
- **API tokens** — `prexel token {list,create,revoke}` para emitir tokens
  long-lived que não dependam do refresh-cookie do browser. Hoje o CLI usa
  o mesmo par access+refresh do navegador.

Quando o agente de paridade entregar um destes, **remova a entrada daqui** e
adicione/atualize a referência em `docs/architecture.md` e `docs/qa-guide.md`.
