# Segurança — Threat model + hardening

Prexel é uma plataforma de deploy self-hosted, então o operador é quem assume o
papel que num PaaS seria do provedor. Este doc descreve o modelo de ameaça que o
v0.1 considera, o que já está mitigado e o que cabe ao operador endurecer.

Para a especificação completa de auth/cripto/rate-limit em formato de produto,
veja [Security & Communication](https://hotsed.atlassian.net/wiki/spaces/Prexel/pages/1310722)
no Confluence — este doc é o complemento operacional.

---

## 1. Modelo de ameaça

Os atacantes que o v0.1 considera, em ordem de probabilidade:

| Ator                       | Capacidade                                                            | O que o Prexel assume                                                                                     |
|----------------------------|-----------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------|
| **Internet anônima**       | Bate em `:443` / `:3000`, scaneia, tenta `admin/admin`.               | Tem que rebater rate limit, TLS, JWT, password policy. Setup wizard fecha após 1ª execução.               |
| **Usuário do mesmo tenant**| Tem conta válida, tenta escalar para outro time/projeto.              | RBAC por time; handler verifica `target.TeamID` antes de qualquer leitura/escrita de app/server/secret.   |
| **Operador comprometido**  | Tem acesso shell ao VPS.                                              | Fora do escopo de defesa. Quem tem o disco lê tudo: a única coisa que o Prexel cifra é secrets at-rest.   |
| **Repo malicioso**         | Dockerfile / docker-compose que tenta escapar do container.           | Fora do escopo no v0.1 — o operador escolhe que código deployar; rede `prexel-net` isola entre apps.      |
| **Servidor remoto comprometido (SSH)** | Atacante substitui daemon Docker no host gerenciado.        | TOFU no fingerprint do host SSH; rotação manual de chave + re-test do servidor.                           |

---

## 2. Mitigações já implementadas

### Autenticação

- **JWT HS256** com TTL curto (~15min) para access token. Chave em
  `PREXEL_JWT_SECRET` (≥ 32 chars, validado no boot).
- **Refresh token rotacionado por família**: cada refresh emite um novo, marca o
  antigo como usado; reuse de um token antigo invalida a família inteira
  (`internal/auth/refresh.go`). Logado como evento de segurança.
- **Cookie `refresh_token`** `HttpOnly` + `Secure` + `SameSite=Strict` + escopo
  `Path=/api/v1/auth` — não fica acessível ao JS, não vaza em XSS.
- **Política de senha** mínima 12 chars + classes mistas
  (`internal/auth/password_validation.go`).
- **2FA TOTP** opcional (Google Authenticator / 1Password) + 8 recovery codes
  one-shot. Login com 2FA gera um `challenge_id` intermediário; 5 tentativas
  erradas invalidam o challenge.
- **TOFU no CLI**: `prexel login` salva o fingerprint do cert self-signed em
  `~/.prexel/known_hosts` no 1º acesso; mismatch posterior bloqueia com erro
  claro.

### Cripto at-rest

- **AES-256-GCM** com HKDF-derived key (`internal/crypto/aes.go`). Round-trip,
  nonce uniqueness e tamper detection cobertos em teste.
- **Secrets, SSH private keys, GitHub App private keys, TOTP seeds, recovery
  codes (hash bcrypt)** são todos guardados cifrados na SQLite.
- **Chave mestra** vem de `PREXEL_SECRET_KEY` (≥ 32 chars, validado no boot).

### Rede / transporte

- TLS em produção via **Caddy + Let's Encrypt** automático, redirect HTTP →
  HTTPS.
- Em dev: cert self-signed gerado em `${PREXEL_DATA_DIR}/tls/` no 1º boot.
- **Security headers** no API server: HSTS, X-Content-Type-Options,
  X-Frame-Options, CSP estrito, Referrer-Policy.
- **CORS** allowlist explícita por `PREXEL_ALLOWED_ORIGINS` — request sem
  Origin combinada não recebe `Access-Control-Allow-*`.

### API hardening

- **Rate limit no login**: 5 tentativas / 15min / IP — depois 429
  (`apimiddleware.LoginRateLimit`).
- **Request size cap**: payloads > 1MB são rejeitados antes de chegar ao
  handler.
- **SetupGate**: rotas do setup wizard retornam 404 após o admin ser criado
  (zero-day de "alguém abre o painel antes do operador").
- **RBAC por time**: cada handler de app/server/secret/domain chama
  `authorizeTeam(perm, teamID)` antes de qualquer side-effect.
- **Defesa em profundidade** no container detail: além do RBAC por app, o
  handler confere `prexel.app_id` label no inspect — evita fishing por
  guessing de container name num daemon Docker compartilhado.

### Rede entre containers

- Apps gerenciados são colocados na bridge `prexel-net`; Caddy roteia
  domínios → `prexel-<app>` sem expor portas no host (default-deny pra outside
  world salvo o que Caddy publica).

---

## 3. Recomendações ao operador

### 3.1 Proxy / front

- Se rodar atrás de um reverse proxy (Cloudflare, nginx corporativo), defina
  **`PREXEL_TRUSTED_PROXIES`** com a CIDR do proxy. Sem isso o rate-limit por
  IP rate-limita o IP do proxy, e o IP real do cliente fica gravado errado nos
  logs de segurança.
- **NUNCA exponha porta 2019** (Caddy admin API) externamente — ela permite
  reconfigurar o roteamento. Em dev, está em `localhost:2019`; em prod, o
  systemd unit só a faz disponível na loopback.

### 3.2 Identidade

- **Habilite 2FA** para todas as contas admin (`/settings → Segurança`).
- Quando v0.2 entregar SSO (OIDC), prefira-o sobre senha local.
- Rotacione recovery codes anualmente ou após qualquer uso.

### 3.3 Segredos

- **Rotação de `PREXEL_SECRET_KEY`** quebra todos os secrets cifrados
  existentes — não é um cenário sem dor no v0.1. Procedure:
  1. `prexel admin backup --out /tmp/old.tar.gz`.
  2. Exporte secrets em plaintext via API enquanto a chave antiga ainda está
     ativa (script ad-hoc; v0.2 vai ter `prexel admin rekey`).
  3. Atualize `PREXEL_SECRET_KEY` em `/etc/prexel/prexel.env`.
  4. `systemctl restart prexel`.
  5. Re-injete cada secret via `prexel secrets <app> set KEY=VALUE`.
- **`PREXEL_JWT_SECRET` pode ser rotacionada a qualquer momento** — só invalida
  todas as sessões ativas (usuários fazem login de novo).
- **Backups criptografados off-site**: o backup tarball inclui a SQLite com os
  secrets já cifrados, mas o tarball em si não é cifrado — passe por
  `age`/`gpg` antes de mandar pro S3.

### 3.4 Rede

- **Network policy no `prexel-net`** (Docker network create com `--internal` ou
  iptables manuais) caso queira proibir saída pra internet por padrão dos
  containers de app. v0.1 deixa egress liberado.
- **Firewall do host**: feche tudo salvo 80/443 e a porta SSH do operador.
  Porta 3000 (API direta) só precisa estar aberta se você NÃO usa o Caddy
  embutido.
- **SSH para servidores remotos**: a chave gerada pelo Prexel é Ed25519, só
  fica no banco (cifrada). Rode `prexel server test <name>` periodicamente
  para detectar mismatch de host key (TOFU registra o 1º; mudança subsequente
  falha).

### 3.5 Build-time secrets

- Build args do Docker que carregam secret **aparecem em `docker history`** da
  imagem final. Workaround:
  - Use `--mount=type=secret` (BuildKit) no Dockerfile; o Prexel passa
    secrets marcados `build_time=true` por essa interface — eles não ficam em
    layer.
  - Veja `docs/qa-guide.md §7.2` para o caminho exato.

---

## 4. Vulnerabilidades conhecidas / limites do MVP

| Item                                                     | Status                                                                              |
|----------------------------------------------------------|-------------------------------------------------------------------------------------|
| Build args sem BuildKit vazam em `docker history`        | Documentado acima. Use `--build-time` + BuildKit mount.                             |
| Sem audit log persistente                                | Eventos de segurança vão pro slog stdout; coletor externo (Loki/journald) opcional. |
| `PREXEL_SECRET_KEY` rotation manual                      | `prexel admin rekey` está em v0.2.                                                  |
| 2FA via CLI (`prexel login` não trata `requires_2fa`)    | Workaround em `docs/qa-guide.md`. CLI parity em v0.2.                               |
| Backup tarball não é cifrado por padrão                  | Use `age`/`gpg` antes do upload off-site.                                           |
| `eventbus` prefix subscriptions só funcionam em replay   | Não vaza dados — só atrasa eventos com wildcard. v0.2.                              |
| Repo malicioso pode escapar do container                 | Fora do escopo. Container isolation = Docker isolation; use gVisor/Kata pra paranoia. |
| Server SSH key rotation precisa de re-test manual        | `prexel server rotate-key` planejado para v0.2.                                     |

---

## 5. Como reportar uma vulnerabilidade

- Email **security@prexel.dev** (PGP key disponível na home do projeto).
- Ou abra um **GitHub Security Advisory** privado no repo `prexel/prexel`.
- Para issues que afetam dados do usuário, comprometem auth ou permitem RCE,
  responda dentro de 24h e tente publicar fix em < 7 dias.
- Não abra issue público até o fix sair em release.

Cripto, refresh-token rotation e RBAC têm testes unitários — qualquer
proposta de mudança nesses arquivos requer review de segurança.
