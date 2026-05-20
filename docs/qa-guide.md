# QA Guide — Prexel MVP v0.1

Roteiro de testes manuais para validar tudo que entrou no MVP. Feito para você executar como QA: cada seção tem **pré-condições**, **passos** e **critério de aceite**.

Estimativa: **50–75 minutos** se rodar tudo (inclui 2FA). Pode pular seções marcadas com **(opcional)**.

> **Nota da v0.2 do guia:** o frontend foi reescrito em cima da guideline visual do **paxel-quote** com paleta roxa Prexel (`#6C47FF`), sidebar pill flutuante, AuthShell com background gradient + dots, fonte Inter, dark mode default. Aceite com **Cmd+K** para abrir o Command Palette em qualquer tela autenticada.

> **2FA:** o backend agora suporta TOTP (Google Authenticator / Authy / 1Password) + recovery codes. Ao habilitar 2FA, **o login passa a exigir um código antes de emitir o JWT**. Veja seção **3.5** para o fluxo completo.

---

## 0. Pré-requisitos

- Docker Desktop rodando.
- Portas livres no host: `80`, `443`, `3443`, `2019`, `5173`.
- Repositório em `~/Dev/prexel` (este).
- Terminal aberto na raiz do repo.

Em **outra aba** do navegador, deixe aberto:
- `https://localhost:3443` (backend HTTPS — vai ter aviso self-signed, **aceite uma vez**)
- `http://localhost:5173` (Vite dev — frontend Vue em dev)
- `http://localhost:2019/config/` (admin API do Caddy — útil pra debugar rotas)

---

## 1. Subir o ambiente

```bash
make up
```

**Aguarde ~20s** na primeira vez (air baixa deps Go + compila). Acompanhe logs:

```bash
make logs       # tail dos 3 services
# OU
docker compose -f deployments/docker-compose.yml logs -f prexel
```

### Critério de aceite

```bash
curl -k -s https://localhost:3443/api/v1/healthz
# → {"status":"ok"}

docker compose ls --all | grep prexel
# → prexel    running(3)   .../docker-compose.yml
```

No Docker Desktop deve aparecer **1 stack único chamado `prexel`** (não `deployments`). As apps que você deployar depois vão ser containers separados (label `prexel.managed=true`), mas o **dev stack** é um só.

---

## 2. Setup wizard via Web UI

> **Atenção:** se o stack já passou pelo setup (o admin `admin@test.com` foi criado durante os smokes), o wizard vai retornar 404. Para refazer o setup do zero, **resete o DB primeiro**:
> ```bash
> docker compose -f deployments/docker-compose.yml down -v
> make up
> # aguarda healthz 200
> ```

### Passos

1. Abra **`http://localhost:5173`** no browser.
2. Você deve ser redirecionado para `/setup`.
3. **Passo 1 (Welcome):** verifique que aparece `instance_id` (`prx_xxxx...`) e versão `0.1.0-dev`. Clique **Começar**.
4. **Passo 2 (Admin):**
   - Email: `admin@test.com`
   - Senha: `SuperSecret123!`
   - Confirme.
   - Tente uma senha fraca (`abc123`) — deve aparecer erro inline ("Senha precisa…").
   - Confirme com senha forte → **Continuar**.
5. **Passo 3 (Instance URL):**
   - Selecione "Usar IP por enquanto (sem SSL)".
   - **Continuar**.
6. **Passo 4 (Servidor):**
   - "Este servidor (local)" — deve mostrar ✓ Docker detectado.
   - **Testar e Continuar**.
7. **Passo 5 (Concluído):** resumo aparece. Clique **Ir para o Dashboard** → redireciona para `/login`.

### Critério de aceite

- Tentar acessar `https://localhost:5173/setup` de novo → **redireciona para `/login`** (setup já completou).
- `curl -k https://localhost:3443/api/v1/setup/status` → `{"completed":true,...}`.

---

## 3. Login

### 3.1 Login Web UI

1. Em `/login`, tente senha errada → mensagem genérica "Email ou senha incorretos".
2. **Repita o erro 5 vezes seguidas** (mesma sessão/IP) → no 6º deve dar **HTTP 429** ou mensagem de rate limit.
3. Aguarde **15 minutos** (ou reinicie o stack) e tente com a senha certa.
4. Acertou → vai para `/apps` (dashboard).
5. **Inspecione DevTools → Application → Cookies** em `localhost:5173`. Deve ter:
   - `refresh_token` com `HttpOnly`, `SameSite=Strict`, `Secure`, `Path=/api/v1/auth`.
6. **Inspecione DevTools → Application → Local Storage**: **não deve ter** o access token armazenado lá (fica em memória do JS).

### 3.2 Login CLI com TOFU (Trust On First Use)

```bash
make ctl ARGS="login --url https://prexel:3000 --email admin@test.com --password SuperSecret123!"
```

**Esperado:** prompt mostrando "Server prexel presented a self-signed certificate. Fingerprint: sha256-…" e pedindo confirmação. Aceite com `y`.

**2ª vez:** rode de novo. Deve passar **sem prompt** (já confiou no fingerprint, salvo em `~/.prexel/known_hosts` do container).

### 3.3 CLI básicos

```bash
make ctl ARGS="whoami"
# → instance_id, version, completed=true, user_id

make ctl ARGS="version"

make ctl ARGS="logout"
make ctl ARGS="whoami"
# → erro "Run 'prexel login' first"
```

### Critério de aceite

- Login Web ✓, rate limit ✓, cookie configurado ✓.
- Login CLI faz TOFU prompt no 1º, silencioso no 2º.
- Logout limpa tokens.

> **Não esqueça de logar de novo** antes das próximas seções: `make ctl ARGS="login --url https://prexel:3000 --email admin@test.com --password SuperSecret123!"`.

---

## 3.5 Two-Factor Authentication (2FA TOTP)

Pré-condições: logado na Web UI com admin.

### 3.5.1 Habilitar 2FA

1. Abra `/settings` → aba **Segurança**.
2. Card "Two-Factor Authentication" mostra **Desabilitado**. Clique **Habilitar 2FA**.
3. Dialog abre com:
   - **QR code** (PNG ~200x200) — escaneie no Google Authenticator / Authy / 1Password.
   - **Secret texto** (fallback caso QR não escaneie) — botão **Copiar** funciona.
   - **Input OtpInput** de 6 dígitos (auto-advance, paste-friendly).
4. Abra o app TOTP, escaneie o QR. App mostra entrada **Prexel (admin@test.com)** com código 6 dígitos rotacionando.
5. Digite o código atual no input → **Confirmar**.
6. **Modal final** mostra **8 recovery codes** formato `XXXX-XXXX-XX`:
   - Botão **Baixar .txt** funciona.
   - Botão **Copiar** funciona.
   - Aviso "Esses códigos não serão exibidos novamente". 
7. Clique **Entendi e salvei**.

### 3.5.2 Login com 2FA via Web

1. Faça **logout**.
2. Em `/login`, entre `admin@test.com` + senha → **submeter**.
3. Redireciona para **`/two-factor-challenge?id=<challenge_id>`** (a query tem o ID gerado).
4. Tela mostra:
   - Ícone de escudo no topo.
   - Card "Use o código do app" (método primário).
   - OtpInput 6 dígitos.
   - Link "Usar código de recuperação" (alterna para input texto).
5. Digite código TOTP atual do app → submete automaticamente no 6º dígito.
6. Sucesso → redireciona para `/apps`.

### 3.5.3 Código errado

1. Repita login. Na tela de challenge digite **6 dígitos errados** (ex: `000000`).
2. Esperado: OtpInput fica vermelho, message "Código inválido". Inputs limpam, foco no 1º.
3. Repita 5 vezes errado → ao 6º a tela deve mostrar erro "Sessão expirada / tentativas excedidas". Volta pra `/login`.

### 3.5.4 Recovery code (one-shot)

1. Login → tela de challenge.
2. Clique **Usar código de recuperação**.
3. Cole um dos 8 codes que você salvou (ex: `ABCD-EFGH-IJ`).
4. Enter → autentica → `/apps`.
5. **Faça logout** + login + challenge **com o mesmo recovery code** → deve falhar (code consumido).
6. Em `/settings/seguranca`, contador "Códigos restantes" deve mostrar `7 de 8`.

### 3.5.5 Regenerar recovery codes

1. Em `/settings/seguranca`, clique **Regenerar códigos de recuperação**.
2. Dialog pede **senha atual** → submeta.
3. Mostra **8 novos codes**. Os antigos (incluindo os não usados) **não funcionam mais**.

### 3.5.6 Desabilitar 2FA

1. Em `/settings/seguranca`, clique **Desabilitar 2FA**.
2. Dialog pede **senha atual + código TOTP atual**.
3. Submeta → 204. Card volta para estado "Desabilitado".
4. Faça logout + login → não precisa mais de challenge.

### 3.5.7 Via curl (smoke do fluxo)

```bash
# Login do user que JÁ tem 2FA
RESP=$(curl -k -s -X POST https://localhost:3443/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@test.com","password":"SuperSecret123!"}')
echo $RESP
# → {"requires_2fa":true,"challenge_id":"<uuid>","methods":["app","recovery"]}

CHALLENGE=$(echo $RESP | jq -r .challenge_id)
TOTP=123456  # substitua pelo código atual do app

curl -k -s -X POST https://localhost:3443/api/v1/auth/2fa/challenge \
  -H 'Content-Type: application/json' \
  -c /tmp/c.txt \
  -d "{\"challenge_id\":\"$CHALLENGE\",\"code\":\"$TOTP\",\"method\":\"app\"}"
# → 200 com access_token + cookie refresh_token
```

### Critério de aceite

- Habilitar 2FA mostra QR + secret + confirm flow.
- Recovery codes exibidos 1x, downloadable.
- Login com 2FA bloqueia até código válido.
- 5 tentativas erradas invalidam challenge.
- Recovery code é one-shot (não funciona 2x).
- Regenerar invalida todos os codes anteriores.
- Disable exige senha + TOTP atual.
- Após disable, login volta a emitir JWT direto.

---

## 4. Servers (CRUD + test connection)

### 4.1 Listagem inicial (Web UI)

1. Em `/servers`, deve haver **1 servidor `local`** (criado no setup) com status verde "connected" e Docker version (algo como 29.x).
2. Clique em **Testar conexão**. Deve mostrar checklist animada com ✓ em todos os checks.

### 4.2 Adicionar servidor remoto (stub — sem testar SSH real)

Via Web UI:

1. Em `/servers`, clique **Adicionar Servidor**.
2. Selecione **Remoto**.
3. Preencha:
   - Nome: `staging`
   - Host: `1.2.3.4` (IP fake)
   - Porta: `22`
   - Usuário: `ubuntu`
   - Chave: **Gerar nova**.
4. Dialog mostra **public key Ed25519** (`ssh-ed25519 AAAA…`) — botão "Copiar" funciona.
5. Salve (sem testar — staging fictício). Server aparece com status `unknown`.
6. Clique **Testar conexão** — deve falhar com erro claro (DNS / timeout TCP), **mas a UI não trava**.

### 4.3 Via CLI

```bash
make ctl ARGS="server list"
make ctl ARGS="server info local"
make ctl ARGS="server test local"

# Adicionar remoto stub
make ctl ARGS="server add --name staging2 --type remote --host 1.2.3.4 --port 22 --user ubuntu --generate-key"
make ctl ARGS="server list"

# Remover (deve falhar se tem apps)
make ctl ARGS="server remove staging2 --force"
```

### Critério de aceite

- Local: connected + Docker version detectada.
- Remote stub: criado com status unknown + public key Ed25519 visível.
- Test connection do remote falha graciosamente.
- `private_key` **nunca aparece** na resposta da API (confira pela aba Network do browser).

---

## 5. Git Sources (3 tipos)

Pré-condições: logado.

### 5.1 SSH key (gerada pelo Prexel)

Web UI: vá em **Git Sources** (se a UI não tem dedicado, use CLI):

```bash
make ctl ARGS="gitsources add ssh-key --name minha-ssh --generate"
# → Imprime public key
```

Anote a public key.

### 5.2 PAT (Personal Access Token)

```bash
make ctl ARGS="gitsources add token --name meu-pat --token ghp_fakebutnotempty12345"
```

### 5.3 GitHub App (opcional — exige registro externo)

GitHub App da org Prexel **ainda não está registrado**. Se você quiser testar OAuth real:

1. Crie um GitHub App na sua conta seguindo `docs/development.md`.
2. Defina env vars no compose:
   ```yaml
   PREXEL_GITHUB_APP_ID: "123456"
   PREXEL_GITHUB_APP_PRIVATE_KEY: |
     -----BEGIN RSA PRIVATE KEY-----
     ...
   PREXEL_GITHUB_APP_SLUG: "meu-app"
   ```
3. `make down && make up`.
4. Via UI: clique "Conectar GitHub" → abre `/git-sources/github/install-url` → vai para GitHub → instala → callback retorna `installation_id`.

**Se não quiser registrar nada:**

```bash
make ctl ARGS="gitsources add github" 
# → deve retornar erro "github_app_not_configured"
```

### 5.4 Listar e remover

```bash
make ctl ARGS="gitsources list"
# → tabela com type/name, credenciais MASCARADAS

make ctl ARGS="gitsources test meu-pat https://github.com/torvalds/linux"
# → ls-remote (pode falhar se PAT for fake — checa que o erro vem da camada git, não 500)
```

### Critério de aceite

- Listagem mascara credenciais (sem token/private_key plaintext).
- SSH key: public key Ed25519 visível.
- GitHub App: install-url retorna erro claro se não configurado.

---

## 6. Apps (criar dockerfile + docker_image)

### 6.1 App docker_image (mais rápido — nginx pronto)

Web UI:

1. Em `/apps`, clique **Nova App**.
2. Preencha:
   - Nome: `nginx-qa`
   - Servidor: local
   - Tipo de build: **Docker Image**
   - Image: `nginx`
   - Tag: `alpine`
   - Porta: `80`
3. **Criar**. App aparece com status `idle`.

CLI alternativa:

```bash
make ctl ARGS="app create --name nginx-qa --server local --build-type docker_image --image-name nginx --image-tag alpine --port 80"
```

### 6.2 App dockerfile (clone + build)

```bash
make ctl ARGS="app create --name hello-go --server local --build-type dockerfile --repo-url https://github.com/dockersamples/single-dev-env --branch main --port 8080"
```

Esse repo público tem Dockerfile simples. Não precisa de credencial git source.

### 6.3 Validações

Tente criar com dados ruins (CLI ou via curl):

```bash
# Nome com espaço → 400
curl -k -s -X POST https://localhost:3443/api/v1/apps -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{"name":"app teste","server_id":"...","build_type":"dockerfile"}'
```

(Pegue `$TOKEN` de DevTools → Network → uma request → header Authorization.)

### Critério de aceite

- Criação OK com dados válidos.
- Validações: nome com espaço, port inválida, build_type errado → 400 com mensagem clara.
- `app list` mostra status `idle` para ambas.

---

## 7. Secrets + Env vars

### 7.1 Set runtime secret

```bash
APP=$(make ctl ARGS="app info nginx-qa" | grep "^id:" | awk '{print $2}')
# OU pegue via Web UI → detalhes → URL

make ctl ARGS="secrets nginx-qa set DATABASE_URL=postgres://fake:fake@host/db"
make ctl ARGS="secrets nginx-qa set MY_API_KEY=verysecret123"
```

### 7.2 Set build-time secret

```bash
make ctl ARGS="secrets nginx-qa set BUILD_TOKEN=abc123 --build-time"
```

### 7.3 List

```bash
make ctl ARGS="secrets nginx-qa list"
# → 3 entradas, valores MASCARADOS (***), build-time/runtime na coluna
```

Web UI: tab **Environment** → seção Secrets. Valores **nunca aparecem** — só keys + flags.

### 7.4 Criptografia no banco

```bash
docker compose -f deployments/docker-compose.yml exec prexel sh -c "sqlite3 /var/lib/prexel/prexel.db 'SELECT key, hex(value) FROM secrets LIMIT 3;'"
```

**Esperado:** `value` é binário hex (ciphertext + nonce AES-GCM). **Nunca** plaintext.

### 7.5 Env vars (não-sensíveis)

```bash
make ctl ARGS="env nginx-qa set NODE_ENV=production"
make ctl ARGS="env nginx-qa set LOG_LEVEL=info"
make ctl ARGS="env nginx-qa list"
# → valores VISÍVEIS (não são secrets)
```

### 7.6 Env sync (diferencial Coolify)

```bash
cat > /tmp/test.env <<EOF
FOO=bar
BAZ=qux
NODE_ENV=development
EOF

docker compose -f deployments/docker-compose.yml run --rm -v /tmp/test.env:/tmp/test.env prexel-cli env nginx-qa sync --file /tmp/test.env
# → "+2, ~1, =0" (added FOO/BAZ, updated NODE_ENV)
```

### Critério de aceite

- Secrets mascarados em list e na UI.
- DB tem ciphertext.
- Env vars visíveis e editáveis.
- Sync não deleta keys ausentes do arquivo.

---

## 8. Domains + DNS + SSL

### 8.1 Adicionar domínio local

```bash
make ctl ARGS="domain add nginx-qa nginx-qa.localhost --primary"
```

Ou via UI: tab Domains → Adicionar.

### 8.2 Status DNS/SSL

```bash
make ctl ARGS="domain status nginx-qa"
```

**Em modo dev (`tls_mode=self-signed` configurado no setup):** o SSL monitor força status `active` com expira em 90d (simulado). DNS check fica `pending` (porque `nginx-qa.localhost` não resolve para IP público).

### 8.3 Verificar route no Caddy

```bash
curl -s http://localhost:2019/config/apps/http/servers/srv0/routes | head -50
```

Deve ter uma route com `match.host=["nginx-qa.localhost"]` e upstream `prexel-nginx-qa:80`.

### 8.4 Retry e Verify

```bash
make ctl ARGS="domain verify nginx-qa nginx-qa.localhost"
# → roda check imediato, log do prexel mostra "dns lookup..."

# Forçar timeout para testar:
docker compose -f deployments/docker-compose.yml exec prexel sqlite3 /var/lib/prexel/prexel.db "UPDATE domains SET dns_check_count=96 WHERE name='nginx-qa.localhost';"
make ctl ARGS="domain status nginx-qa"
# → ssl_status=failed

make ctl ARGS="domain retry nginx-qa nginx-qa.localhost"
# → counter zera, status volta para pending
```

### Critério de aceite

- Route aparece no Caddy.
- DNS check loop incrementa counter a cada ~30s (veja `make logs`).
- Retry zera contador.

---

## 9. Deploy (UI + CLI com --watch)

### 9.1 Deploy via Web UI

1. Em `/apps/<id>`, tab Overview, clique **Deploy**.
2. Dialog abre com **TerminalOutput** SSE — você deve ver linhas chegando em tempo real:
   - "deploy.started"
   - linhas de `docker pull nginx:alpine` (ou build se for dockerfile)
   - "Starting candidate container..."
   - "Health check..."
   - "Routing updated..."
   - "deploy.success ✓" em ~10-20s para docker_image.
3. App status muda para `running` (badge verde).

### 9.2 Deploy via CLI sem --watch

```bash
make ctl ARGS="deploy nginx-qa"
# → "Deploy queued for nginx-qa, deployment_id=..."

# Aguarde ~25s e:
make ctl ARGS="app info nginx-qa"
# → status: running
```

### 9.3 Deploy via CLI com --watch (TUI)

```bash
# Precisa rodar em TTY interativo:
docker compose -f deployments/docker-compose.yml run --rm -it prexel-cli deploy nginx-qa --watch
```

**Esperado:** TUI Bubbletea com 3 fases (Build → Health Check → Swap), spinner por fase, logs em terminal embutido. Sai com código 0 ao sucesso.

### 9.4 Container rodando

```bash
docker ps --filter "label=prexel.app_name=nginx-qa" --format "table {{.Names}}\t{{.Status}}\t{{.Networks}}"
# → prexel-nginx-qa  Up Xs  prexel-net
```

### 9.5 Roteamento via Caddy

```bash
curl -s -H "Host: nginx-qa.localhost" http://localhost/
# → <html> com "Welcome to nginx!"
```

### Critério de aceite

- SSE chega no UI em tempo real.
- App status passa idle → building → deploying → running.
- Container existe na rede `prexel-net`.
- Caddy roteia o domínio → HTTP 200.
- Deployment registrado em `app/<id>/deployments`.

---

## 10. Logs em tempo real

### 10.1 Logs do container via Web UI

1. Em `/apps/<id>/logs`, terminal full-width mostra últimas linhas do container.
2. Faça `curl -H "Host: nginx-qa.localhost" http://localhost/` algumas vezes em outra aba.
3. Novas linhas devem aparecer no terminal em **tempo real** (sem refresh manual).
4. Toggle "Auto-scroll" — desligue, scroll pra cima, ligue de novo → segue automático.

### 10.2 Logs via CLI

```bash
docker compose -f deployments/docker-compose.yml run --rm -it prexel-cli logs nginx-qa --tail 20
# (Ctrl+C para parar)
```

### 10.3 Logs do deployment (build)

Em `/apps/<id>` → tab Deployments → expandir uma linha → deve ver os logs de build/health check daquele deploy.

### Critério de aceite

- Stream SSE chega em tempo real (latência < 1s).
- stdout em cinza claro, stderr em vermelho.
- Auto-scroll funciona.

---

## 11. Rollback

Pré: app `nginx-qa` rodando, **2+ deployments** com status `success`.

```bash
# Faz um 2º deploy
make ctl ARGS="deploy nginx-qa"
sleep 25
make ctl ARGS="deployments nginx-qa"
# → tabela com 2 linhas success
```

### 11.1 Rollback CLI interativo

```bash
docker compose -f deployments/docker-compose.yml run --rm -it prexel-cli rollback nginx-qa
# → TUI lista deploys success, seleciona com setas + Enter, confirma
```

### 11.2 Rollback Web UI

Em `/apps/<id>` → tab Deployments → linha anterior → **Rollback para esta versão** → confirme.

### 11.3 Rollback automático (health check falha)

Crie uma app que vai falhar health check:

```bash
make ctl ARGS="app create --name badhealth --server local --build-type docker_image --image-name nginx --image-tag alpine --port 9999"
# port 9999 ≠ 80, health check vai falhar
make ctl ARGS="deploy badhealth"
sleep 45
make ctl ARGS="app info badhealth"
# → status: error
docker ps --filter "name=prexel-badhealth"
# → vazio (container temp foi limpo)
```

Logs do prexel:

```bash
docker compose -f deployments/docker-compose.yml logs prexel 2>&1 | grep -i "health check" | tail -5
# → "Health check failed after Xs. Rolled back automatically."
```

### Critério de aceite

- Rollback manual restaura imagem antiga sem downtime visível.
- Rollback cria novo deployment com `rollback_of=<dep_id>`.
- Rollback automático mata container novo, preserva o antigo (se existe).

---

## 12. Stop / Restart

```bash
make ctl ARGS="app stop nginx-qa"
docker ps --filter "name=prexel-nginx-qa"
# → vazio

make ctl ARGS="app info nginx-qa"
# → status: stopped

# Restart via CLI ou UI
make ctl ARGS="app restart nginx-qa"
sleep 10
docker ps --filter "name=prexel-nginx-qa"
# → Up Xs
```

---

## 13. Cenários de segurança

### 13.1 Sem auth

```bash
curl -k -s -o /dev/null -w "%{http_code}\n" https://localhost:3443/api/v1/apps
# → 401
```

### 13.2 JWT inválido

```bash
curl -k -s -o /dev/null -w "%{http_code}\n" https://localhost:3443/api/v1/apps -H "Authorization: Bearer fake.jwt.token"
# → 401
```

### 13.3 CORS rejeita outra origem

```bash
curl -k -s -o /dev/null -w "%{http_code}\n" -H "Origin: https://evil.example.com" https://localhost:3443/api/v1/apps
# → ainda 401 (header Origin não bypassa auth)
# Olhe resposta com -v: deve NÃO ter Access-Control-Allow-Origin: evil.example.com
```

### 13.4 Token reuse (família invalidada)

```bash
# Login pegando cookie
curl -k -s -X POST https://localhost:3443/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@test.com","password":"SuperSecret123!"}' \
  -c /tmp/c1.txt -o /tmp/login.json

# Salva cookie original
cp /tmp/c1.txt /tmp/c_orig.txt

# Faz 1º refresh (rotaciona token)
curl -k -s -X POST https://localhost:3443/api/v1/auth/refresh -b /tmp/c1.txt -c /tmp/c1.txt
# → 200, novo cookie em c1

# Tenta refresh com cookie ORIGINAL (já revogado pela rotação)
curl -k -s -o /dev/null -w "%{http_code}\n" -X POST https://localhost:3443/api/v1/auth/refresh -b /tmp/c_orig.txt
# → 401

# Agora tenta usar o cookie c1 (que era válido) → também deve falhar porque a família foi invalidada
curl -k -s -o /dev/null -w "%{http_code}\n" -X POST https://localhost:3443/api/v1/auth/refresh -b /tmp/c1.txt
# → 401 (família revogada após reuse detectado)

docker compose -f deployments/docker-compose.yml logs prexel 2>&1 | grep -i "reuse" | tail -2
# → "refresh token reuse detected — family invalidated"
```

### 13.5 Security headers

```bash
curl -k -s -I https://localhost:3443/api/v1/healthz
# Deve ter: Strict-Transport-Security, X-Content-Type-Options, X-Frame-Options, Content-Security-Policy, Referrer-Policy
```

### 13.6 HTTP → HTTPS redirect

```bash
curl -s -o /dev/null -w "%{http_code} -> %{redirect_url}\n" http://localhost:3443/api/v1/healthz
# (em dev o redirect é interno — Caddy pode interceptar antes; o backend tem o handler)
```

### Critério de aceite

- 401 sem token / token inválido.
- CORS não permite cross-origin com credentials.
- Token reuse invalida família + log de segurança.
- Headers obrigatórios presentes.

---

## 14. Imagem de produção (smoke)

Build a imagem prod (distroless ~73MB) e rode isolada:

```bash
docker build -f build/package/Dockerfile -t prexel-prod:qa .
# ~3-5min na primeira vez

mkdir -p /tmp/prexel-prod-data

docker run --rm -d --name prexel-prod-test \
  -e PREXEL_SECRET_KEY=$(openssl rand -hex 32) \
  -e PREXEL_JWT_SECRET=$(openssl rand -hex 32) \
  -e PREXEL_DATA_DIR=/var/lib/prexel \
  -v /tmp/prexel-prod-data:/var/lib/prexel \
  -p 3001:3000 \
  prexel-prod:qa serve

sleep 8

curl -k -s https://localhost:3001/api/v1/healthz
# → {"status":"ok"}

curl -k -s https://localhost:3001/ | grep -E "(title|prexel)" | head -3
# → <title>Prexel</title> (SPA embutida via go:embed)

curl -k -s https://localhost:3001/setup | grep -E "<title|<div" | head -3
# → mesma SPA (history fallback OK)

docker stop prexel-prod-test
rm -rf /tmp/prexel-prod-data
```

### Critério de aceite

- Imagem prod < 100MB.
- Healthz responde.
- Rota `/` retorna SPA Vue (não 404).
- History fallback (rotas SPA) funciona — qualquer path do Vue Router devolve `index.html`.

---

## 15. Cenários adicionais (opcional)

### 15.1 Server fica disconnected

```bash
# Mata o Docker do "servidor" — mas como é local, isso seria parar o próprio prexel.
# Alternativa: criar um server remote stub e usar host inacessível, depois rodar o status loop manualmente.

make ctl ARGS="server add --name fake-remote --type remote --host 1.2.3.4 --port 22 --user fake --generate-key"
# Aguardar 60s (status loop)
make ctl ARGS="server list"
# → fake-remote com status disconnected
```

### 15.2 Multi-deployment + retenção de imagens

```bash
# Faça 7 deploys da mesma app
for i in 1 2 3 4 5 6 7; do
  make ctl ARGS="deploy nginx-qa"
  sleep 20
done

docker images --filter "label=prexel.app_name=nginx-qa" --format "{{.Repository}}:{{.Tag}}"
# → deve haver no MÁXIMO 5 imagens (retenção configurada)
```

### 15.3 Recuperação de senha (no servidor)

```bash
make ctl ARGS="admin reset-password --email admin@test.com --password NovaSenh@2026!"
make ctl ARGS="login --url https://prexel:3000 --email admin@test.com --password NovaSenh@2026!"
# → autenticado com nova senha
```

(Em produção, esse comando roda direto no servidor, não via CLI client.)

---

## 16. Cleanup

```bash
# Remove apps de teste via CLI
make ctl ARGS="app remove nginx-qa --force"
make ctl ARGS="app remove hello-go --force"
make ctl ARGS="app remove badhealth --force"

# Remove servers stub
make ctl ARGS="server remove staging --force"
make ctl ARGS="server remove staging2 --force"
make ctl ARGS="server remove fake-remote --force"

# Desliga stack
make down

# Wipe completo (apaga DB, secrets, logs):
docker compose -f deployments/docker-compose.yml down -v
```

---

## Resumo: visual da UI (paxel-style)

Pontos a olhar quando estiver navegando:

- **Sidebar pill flutuante** com ícone + dot indicator no item ativo (linha 100% à esquerda do viewport, separada por shadow, **não** se cola na borda).
- **Avatar** no fundo da sidebar com Popover de menu (UserMenu).
- **Topbar de pesquisa** integrado no AppHeader (opcional — pode estar oculto em algumas rotas).
- **CommandPalette** (Cmd+K): abre overlay com search + lista de páginas/ações. Esc fecha. Setas navegam.
- **AuthShell** (login, 2FA, setup): card centralizado com background gradient roxo + dot grid + footer "TLS · SOC2 · LGPD" + copyright.
- **Toast** customizado: ícone redondo tinted por severity (verde success, âmbar warn, vermelho error, teal info).
- **DataTable**: linhas com densidade configurável (compact/comfy/spacious) via preferences store.
- **Dark mode**: default. Inspecione `html.dark` no DOM. Tokens CSS vars (`--p-bg`, `--p-content-bg`, etc.) reagem automaticamente.

Cores devem ser **roxo Prexel** em todo lugar onde teal Paxel apareceria:
- Sidebar item active background: `rgba(108, 71, 255, 0.08)` (light) / `0.18` (dark).
- Botões primários: `#6C47FF` background.
- Focus ring: `rgba(108, 71, 255, 0.2)`.
- Logo: `PrexelMark` (deve aparecer no AuthShell, sidebar logo, header).

Se algo aparecer em teal `#016583`, é bug — reporte.

---

## Resumo: critérios mínimos de aceite do MVP v0.1

Para considerar o MVP **OK para release v0.1**, **TUDO** abaixo deve passar:

- [ ] **Instalar** com one-liner (não testado aqui — testa em VPS real depois)
- [ ] **Setup wizard completo via Web UI em < 3 minutos**
- [ ] Nenhum acesso ao dashboard sem login válido
- [ ] Adicionar servidor local + testar conexão
- [ ] Criar app + adicionar domínio + status `running`
- [ ] Primeiro deploy de Dockerfile **OU** docker_image com sucesso
- [ ] Logs em tempo real no terminal (UI) **E** no CLI
- [ ] App acessível via domínio (route Caddy funcionando) — em dev usando `Host:` header
- [ ] Rollback manual restaura versão anterior
- [ ] Rollback automático em health check failure
- [ ] CLI e UI fazem as mesmas operações
- [ ] Secrets criptografados no DB
- [ ] Token families: reuse invalida família
- [ ] Imagem prod < 100MB com SPA embutida funcionando
- [ ] **2FA**: habilitar com QR → recovery codes → login com TOTP → recovery one-shot → disable
- [ ] **Visual paxel-style**: paleta roxa (`#6C47FF`), sidebar pill, AuthShell com gradient, dark mode default
- [ ] **Cmd+K** abre Command Palette

---

## Bugs/limitações conhecidas (não bloqueiam release)

- `internal/eventbus` prefix subscriptions (`deploy.*`) só funcionam em replay, não em delivery ao vivo.
- Homebrew tap precisa ser criado manualmente antes do release público.
- `get.prexel.dev` precisa ser provisionado para o one-liner funcionar.
- GitHub App OAuth flow não foi testado end-to-end (App da org Prexel não registrada ainda).
- Caddy `CertStatus` retorna stub — em prod real com Let's Encrypt isso vai precisar de implementação concreta no `internal/caddy`.
- **2FA via CLI ainda não suportado**: `prexel login` no CLI ainda não trata o caso `requires_2fa`. Workaround: usar `prexel admin reset-password` no servidor para desabilitar 2FA temporariamente, ou via UI desabilitar e relogar.
- **Sem dark/light toggle visível** na UI v0.1 — dark é o default e persiste em `localStorage`. Para forçar light, abra DevTools console: `localStorage.setItem('prexel:theme','light'); location.reload()`.
- **Sem método 2FA por email** no v0.1 — apenas TOTP (app) + recovery codes.
- **`CommandPalette` não busca apps/servers ainda** — só lista páginas e ações estáticas. Busca dinâmica fica para v0.2.

Mais detalhes em `docs/testing.md` e `docs/release.md`.
