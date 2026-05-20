package composespec

import (
	"sort"
	"strings"
	"testing"
)

// findService picks a service out of Spec.Services by name. Parse
// stores services in arbitrary map-iteration order; tests must
// look up by name rather than indexing.
func findService(t *testing.T, sp *Spec, name string) *Service {
	t.Helper()
	for i := range sp.Services {
		if sp.Services[i].Name == name {
			return &sp.Services[i]
		}
	}
	t.Fatalf("service %q not in spec", name)
	return nil
}

func TestParse_EmptyDocumentRejected(t *testing.T) {
	if _, err := Parse(nil); err == nil {
		t.Error("expected error for nil input")
	}
	if _, err := Parse([]byte{}); err == nil {
		t.Error("expected error for empty input")
	}
}

func TestParse_InvalidYAMLRejected(t *testing.T) {
	// Unbalanced bracket — yaml.Unmarshal returns a parse error.
	if _, err := Parse([]byte("services: [")); err == nil {
		t.Error("expected error for malformed yaml")
	}
}

func TestParse_NoServicesRejected(t *testing.T) {
	// Valid YAML, but lacks the `services:` map entirely.
	if _, err := Parse([]byte("version: '3'\n")); err == nil {
		t.Error("expected error when no services declared")
	}
}

func TestParse_ImageOnlyService(t *testing.T) {
	// Minimal valid service: just `image:` — no build block, no env,
	// no command, no depends_on. Locks down the happy path.
	doc := `
services:
  web:
    image: nginx:alpine
`
	sp, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(sp.Services) != 1 {
		t.Fatalf("len(Services) = %d, want 1", len(sp.Services))
	}
	svc := findService(t, sp, "web")
	if svc.Image != "nginx:alpine" {
		t.Errorf("Image = %q, want nginx:alpine", svc.Image)
	}
	if svc.HasBuild {
		t.Errorf("HasBuild = true on image-only service")
	}
	if svc.Build != nil {
		t.Errorf("Build = %+v, want nil on image-only service", svc.Build)
	}
}

func TestParse_BuildShortForm(t *testing.T) {
	// `build: ./path` — string scalar becomes BuildSpec{Context: "./path"}.
	doc := `
services:
  web:
    build: ./backend
`
	sp, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	svc := findService(t, sp, "web")
	if !svc.HasBuild {
		t.Error("HasBuild = false on build-short-form service")
	}
	if svc.Build == nil {
		t.Fatal("Build is nil after short-form parse")
	}
	if svc.Build.Context != "./backend" {
		t.Errorf("Build.Context = %q, want ./backend", svc.Build.Context)
	}
	if svc.Build.Dockerfile != "" {
		t.Errorf("Build.Dockerfile = %q on short form, want empty", svc.Build.Dockerfile)
	}
}

func TestParse_BuildLongForm(t *testing.T) {
	// Long form with context + dockerfile + args (mapping syntax for args).
	doc := `
services:
  api:
    build:
      context: ./svc
      dockerfile: Dockerfile.prod
      args:
        NODE_ENV: production
        API_URL: https://api.example.com
`
	sp, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	svc := findService(t, sp, "api")
	if svc.Build == nil {
		t.Fatal("Build nil after long-form parse")
	}
	if svc.Build.Context != "./svc" {
		t.Errorf("Build.Context = %q, want ./svc", svc.Build.Context)
	}
	if svc.Build.Dockerfile != "Dockerfile.prod" {
		t.Errorf("Build.Dockerfile = %q, want Dockerfile.prod", svc.Build.Dockerfile)
	}
	if svc.Build.Args["NODE_ENV"] != "production" {
		t.Errorf("Build.Args[NODE_ENV] = %q, want production", svc.Build.Args["NODE_ENV"])
	}
	if svc.Build.Args["API_URL"] != "https://api.example.com" {
		t.Errorf("Build.Args[API_URL] = %q", svc.Build.Args["API_URL"])
	}
}

func TestParse_EnvironmentMapForm(t *testing.T) {
	// `environment:` as a mapping — values can be strings, numbers, bools.
	doc := `
services:
  web:
    image: nginx:alpine
    environment:
      FOO: bar
      DEBUG: "true"
      PORT: "8080"
`
	sp, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	svc := findService(t, sp, "web")
	if svc.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want bar", svc.Env["FOO"])
	}
	if svc.Env["DEBUG"] != "true" {
		t.Errorf("Env[DEBUG] = %q, want true", svc.Env["DEBUG"])
	}
	if svc.Env["PORT"] != "8080" {
		t.Errorf("Env[PORT] = %q, want 8080", svc.Env["PORT"])
	}
}

func TestParse_EnvironmentListForm(t *testing.T) {
	// `environment:` as a list of "KEY=VALUE" strings.
	doc := `
services:
  web:
    image: nginx:alpine
    environment:
      - FOO=bar
      - DEBUG=true
      - BARE
`
	sp, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	svc := findService(t, sp, "web")
	if svc.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want bar", svc.Env["FOO"])
	}
	if svc.Env["DEBUG"] != "true" {
		t.Errorf("Env[DEBUG] = %q, want true", svc.Env["DEBUG"])
	}
	if _, ok := svc.Env["BARE"]; !ok {
		t.Errorf("bare env key BARE missing — want present with empty value")
	}
}

func TestParse_CommandStringAndList(t *testing.T) {
	// Lock down both `command:` shapes — string is field-split,
	// list is taken verbatim.
	doc := `
services:
  one:
    image: alpine
    command: echo hello world
  two:
    image: alpine
    command: ["echo", "hello world"]
`
	sp, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	one := findService(t, sp, "one")
	if len(one.Command) != 3 || one.Command[0] != "echo" || one.Command[2] != "world" {
		t.Errorf("string command = %v, want [echo hello world]", one.Command)
	}
	two := findService(t, sp, "two")
	if len(two.Command) != 2 || two.Command[1] != "hello world" {
		t.Errorf("list command = %v, want [echo, \"hello world\"]", two.Command)
	}
}

func TestParse_DependsOnShortAndLongForm(t *testing.T) {
	// depends_on accepts both `[name, name]` and `{name: {condition: ...}}`.
	doc := `
services:
  web:
    image: nginx
    depends_on: [db, cache]
  api:
    image: alpine
    depends_on:
      db:
        condition: service_healthy
      cache:
        condition: service_started
`
	sp, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	web := findService(t, sp, "web")
	sort.Strings(web.DependsOn)
	if len(web.DependsOn) != 2 || web.DependsOn[0] != "cache" || web.DependsOn[1] != "db" {
		t.Errorf("short depends_on = %v, want [cache db]", web.DependsOn)
	}
	api := findService(t, sp, "api")
	sort.Strings(api.DependsOn)
	if len(api.DependsOn) != 2 || api.DependsOn[0] != "cache" || api.DependsOn[1] != "db" {
		t.Errorf("long depends_on = %v, want [cache db]", api.DependsOn)
	}
}

func TestParse_RestartPolicy(t *testing.T) {
	// Compose accepts `no | always | on-failure | unless-stopped` —
	// we copy the string through verbatim, validation is at deploy time.
	doc := `
services:
  a:
    image: alpine
    restart: always
  b:
    image: alpine
    restart: unless-stopped
  c:
    image: alpine
    restart: on-failure
`
	sp, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]string{"a": "always", "b": "unless-stopped", "c": "on-failure"}
	for name, w := range want {
		if got := findService(t, sp, name).Restart; got != w {
			t.Errorf("svc %s restart = %q, want %q", name, got, w)
		}
	}
}

func TestParse_PortsShortAndLongAndExpose(t *testing.T) {
	// Cover every port-spec shape collectPorts knows:
	//   - bare container port            -> "80"
	//   - host:container short syntax    -> "8080:80"
	//   - host:container with bind IP    -> "127.0.0.1:8081:81"
	//   - container with proto suffix    -> "82/udp"
	//   - range (we take the start)      -> "8090-8092:90"
	//   - long-form mapping              -> { target: 91, ... }
	//   - `expose:` entries              -> container-only ports
	doc := `
services:
  web:
    image: nginx
    ports:
      - "80"
      - "8080:80"
      - "127.0.0.1:8081:81"
      - "82/udp"
      - "8090-8092:90"
      - target: 91
        published: 9091
        protocol: tcp
    expose:
      - "92"
`
	sp, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	svc := findService(t, sp, "web")
	got := make(map[int]bool, len(svc.Ports))
	for _, p := range svc.Ports {
		got[p] = true
	}
	for _, want := range []int{80, 81, 82, 90, 91, 92} {
		if !got[want] {
			t.Errorf("port %d missing from Ports %v", want, svc.Ports)
		}
	}
}

func TestParse_HasHealthcheck(t *testing.T) {
	// We don't surface the healthcheck details — just whether one was
	// declared. The flag drives a badge in the UI.
	doc := `
services:
  with:
    image: nginx
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost/"]
  without:
    image: nginx
`
	sp, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !findService(t, sp, "with").HasHealthcheck {
		t.Error("HasHealthcheck = false on healthcheck-bearing service")
	}
	if findService(t, sp, "without").HasHealthcheck {
		t.Error("HasHealthcheck = true on plain service")
	}
}

func TestExtractContainerPort_EdgeCases(t *testing.T) {
	// extractContainerPort is the workhorse for port short syntax —
	// table-test the corners so we don't regress on the "last colon
	// segment, before any slash" rule documented in the parser.
	cases := map[string]int{
		"":              0,
		"   ":           0,
		"80":            80,
		"8080:80":       80,
		"127.0.0.1:1:2": 2,
		"3000/tcp":      3000,
		"8080-8082:80":  80,
		"8080-8082":     8080, // bare range -> start of range
		"notaport":      0,
	}
	for in, want := range cases {
		if got := extractContainerPort(in); got != want {
			t.Errorf("extractContainerPort(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestCollectPorts_Dedup(t *testing.T) {
	// Duplicate ports from `ports:` + `expose:` collapse to a single entry.
	// We assemble the YAML rather than calling collectPorts directly to
	// keep the test honest about the public surface.
	doc := `
services:
  web:
    image: nginx
    ports:
      - "80"
      - "8080:80"
    expose:
      - "80"
`
	sp, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	svc := findService(t, sp, "web")
	count := 0
	for _, p := range svc.Ports {
		if p == 80 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("port 80 appears %d times, want 1 (deduped): %v", count, svc.Ports)
	}
}

func TestParse_MultiServiceMix(t *testing.T) {
	// One file, three flavours of service to exercise the loop body:
	// pulled image, locally built, and a service with both env + ports.
	doc := `
services:
  db:
    image: postgres:15
    environment:
      POSTGRES_PASSWORD: secret
  api:
    build: ./api
    ports:
      - "3000:3000"
    depends_on:
      - db
  worker:
    image: alpine
    command: ["sh", "-c", "echo worker"]
    restart: on-failure
`
	sp, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(sp.Services) != 3 {
		t.Fatalf("len(Services) = %d, want 3", len(sp.Services))
	}
	names := make([]string, 0, len(sp.Services))
	for _, s := range sp.Services {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "api,db,worker" {
		t.Errorf("service names = %s, want api,db,worker", got)
	}
}
