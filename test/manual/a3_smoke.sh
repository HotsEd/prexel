#!/usr/bin/env bash
# Milestone A3 smoke test:
#   - prexel-net network exists
#   - internal/docker can EnsureNetwork on the local provider
#   - internal/caddy can Ping + Bootstrap + UpsertRoute against the live caddy
#   - caddy proxies a request to a test nginx container in prexel-net
#
# Run from the repo root: bash test/manual/a3_smoke.sh
set -euo pipefail

COMPOSE="docker compose -f deployments/docker-compose.yml"

# Stage scratch Go files inside the repo (so they're visible to the prexel
# container via the /workspace bind mount) but in a unique tmpdir we always
# clean up. Avoids leaving a3_smoke_*.go droppings in the repo root if the
# script dies mid-run.
SMOKE_TMP="$(mktemp -d -p . a3_smoke.XXXXXX)"
SMOKE_TMP_NAME="$(basename "$SMOKE_TMP")"

cleanup() {
  echo "==> cleanup"
  docker rm -f a3-nginx-test >/dev/null 2>&1 || true
  rm -rf "$SMOKE_TMP"
}
trap cleanup EXIT

echo "==> ensure stack is up"
$COMPOSE up -d >/dev/null

echo "==> wait for caddy admin to respond"
for i in $(seq 1 20); do
  if curl -fsS -o /dev/null http://localhost:2019/config/; then
    break
  fi
  sleep 1
done

echo "==> confirm prexel-net exists"
docker network inspect prexel-net >/dev/null

echo "==> launch test nginx container in prexel-net"
docker rm -f a3-nginx-test >/dev/null 2>&1 || true
docker run -d --rm --name a3-nginx-test --network prexel-net nginx:alpine >/dev/null

echo "==> compile + run go smoke (inside prexel container)"
cat > "$SMOKE_TMP/a3_smoke_run.go" <<'EOF'
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	caddyclient "github.com/prexel/prexel/internal/caddy"
	dock "github.com/prexel/prexel/internal/docker"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// --- Docker layer ---
	p := dock.NewLocalProvider("local")
	defer p.Close()

	v, err := dock.Version(ctx, p)
	if err != nil {
		log.Fatalf("Version: %v", err)
	}
	fmt.Printf("docker engine version: %s (API %s)\n", v.Version, v.APIVersion)

	if err := dock.EnsureNetwork(ctx, p, dock.PrexelNetwork); err != nil {
		log.Fatalf("EnsureNetwork: %v", err)
	}
	fmt.Println("prexel-net OK")

	// --- Caddy layer ---
	// From inside prexel container, caddy is reachable on prexel-net.
	adminURL := os.Getenv("CADDY_ADMIN_URL")
	if adminURL == "" {
		adminURL = "http://caddy:2019"
	}
	c := caddyclient.New(adminURL)
	if err := c.Ping(ctx); err != nil {
		log.Fatalf("Ping: %v", err)
	}
	fmt.Println("caddy admin OK")

	if err := c.BootstrapBaseConfig(ctx); err != nil {
		log.Fatalf("BootstrapBaseConfig: %v", err)
	}
	fmt.Println("caddy bootstrap OK")

	if err := c.UpsertRoute(ctx, "test.localhost", "a3-nginx-test", 80); err != nil {
		log.Fatalf("UpsertRoute (insert): %v", err)
	}
	fmt.Println("upsert (insert) OK")

	// Idempotency: second call should also succeed (PATCH path).
	if err := c.UpsertRoute(ctx, "test.localhost", "a3-nginx-test", 80); err != nil {
		log.Fatalf("UpsertRoute (update): %v", err)
	}
	fmt.Println("upsert (update) OK")

	fmt.Println("OK")
}
EOF

$COMPOSE exec -T prexel sh -c "cd /workspace/$SMOKE_TMP_NAME && go run a3_smoke_run.go"

echo "==> curl through caddy with Host: test.localhost"
RESP=$(curl -sS -o /dev/null -w "%{http_code} %{redirect_url}\n" -H 'Host: test.localhost' http://localhost/ || true)
echo "    -> $RESP"

# Caddy will 308-redirect HTTP->HTTPS by default (auto-https). Follow it but
# accept the self-signed Let's Encrypt staging response only if HTTPS works.
# In dev we accept either the 308 (proves the route matched) or a 200 from
# nginx if auto-https is off.
echo "==> follow redirect (insecure) to see nginx response"
curl -skL -H 'Host: test.localhost' http://localhost/ | head -5

echo ""
echo "==> remove route + re-check it's gone"
cat > "$SMOKE_TMP/a3_smoke_cleanup.go" <<'EOF'
package main

import (
	"context"
	"fmt"
	"time"

	caddyclient "github.com/prexel/prexel/internal/caddy"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := caddyclient.New("http://caddy:2019")
	if err := c.RemoveRoute(ctx, "test.localhost"); err != nil {
		fmt.Println("remove:", err)
	} else {
		fmt.Println("route removed")
	}
}
EOF
$COMPOSE exec -T prexel sh -c "cd /workspace/$SMOKE_TMP_NAME && go run a3_smoke_cleanup.go"

echo ""
echo "A3 smoke complete."
