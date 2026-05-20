// Package composespec parses the small subset of docker-compose.yml
// the Prexel UI cares about for the "Containers" preview screen.
//
// We deliberately do NOT depend on the official compose-go library:
//   - it pulls in a heavy dependency tree (Compose v2 schema is huge)
//   - we only need names + images + ports + a hint at healthcheck
//   - the runner that actually starts containers will validate the
//     full spec at deploy time; this parser's job is "tell the UI
//     what the operator wrote"
//
// Anything we don't recognise is ignored on purpose — we never want
// the preview to fail because the operator's YAML has fields we
// don't model yet.
package composespec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Service is the trimmed-down per-service projection.
type Service struct {
	Name string `json:"name"`
	// Image is what `docker pull / run` will reference. Empty when
	// the service is built locally (build: .).
	Image string `json:"image,omitempty"`
	// HasBuild is true when the service has a `build:` key — used by
	// the UI to label it "built locally" vs "pulled image".
	HasBuild bool `json:"has_build"`
	// Build carries the parsed `build:` block when present. nil when
	// the service uses `image:` only. Both compose syntaxes — short
	// (`build: .`) and long (`build: { context, dockerfile, args }`)
	// — collapse into this struct.
	Build *BuildSpec `json:"build,omitempty"`
	// Ports lists every port the service declares. Each entry is the
	// *container* port (the target Caddy would route to). Host-side
	// mappings are intentionally dropped because Prexel apps run on
	// the prexel-net bridge and don't expose to the host.
	Ports []int `json:"ports"`
	// HasHealthcheck mirrors whether the YAML defines `healthcheck:`
	// for this service. We don't surface the full config — the runner
	// reads that directly. The UI just wants to know "is there one?".
	HasHealthcheck bool `json:"has_healthcheck"`
	// Env carries the service's `environment:` map. Both list-of-strings
	// (`KEY=VALUE`) and map (`KEY: VALUE`) syntaxes are normalised
	// here. Used by the deploy engine to seed env vars when running
	// the container.
	Env map[string]string `json:"env,omitempty"`
	// Command overrides the image CMD. Pass-through to the container
	// runtime; honoured when non-empty.
	Command []string `json:"command,omitempty"`
	// Restart mirrors `restart:` (no | always | on-failure | unless-stopped).
	// Empty means "fall back to the app-level restart policy".
	Restart string `json:"restart,omitempty"`
	// DependsOn lists service names this service depends on. Used by
	// the deploy engine to start services in topological order so
	// downstream services don't race their upstreams.
	DependsOn []string `json:"depends_on,omitempty"`
}

// Spec is the parsed view of a compose file.
type Spec struct {
	Services []Service `json:"services"`
}

// BuildSpec is the per-service `build:` block. Context is relative to
// the compose file's directory (matches docker-compose semantics).
// Dockerfile is relative to Context. Args carries `build.args:` for
// docker `--build-arg`.
type BuildSpec struct {
	Context    string            `json:"context"`
	Dockerfile string            `json:"dockerfile,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
}

// rawCompose mirrors the YAML structure loosely enough to survive
// version drift (we only assert types on the fields we read).
type rawCompose struct {
	Services map[string]rawService `yaml:"services"`
}

type rawService struct {
	Image       string      `yaml:"image"`
	Build       yaml.Node   `yaml:"build"`
	Ports       []yaml.Node `yaml:"ports"`
	Expose      []yaml.Node `yaml:"expose"`
	Healthcheck yaml.Node   `yaml:"healthcheck"`
	Environment yaml.Node   `yaml:"environment"`
	Command     yaml.Node   `yaml:"command"`
	Restart     string      `yaml:"restart"`
	DependsOn   yaml.Node   `yaml:"depends_on"`
}

// Parse decodes raw YAML bytes into a Spec. Errors only on
// catastrophic shape mismatches (root not a map, etc.) — individual
// service quirks are tolerated and surface as best-effort fields.
func Parse(data []byte) (*Spec, error) {
	if len(data) == 0 {
		return nil, errors.New("compose: empty document")
	}
	var raw rawCompose
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("compose: parse yaml: %w", err)
	}
	if len(raw.Services) == 0 {
		return nil, errors.New("compose: no services declared")
	}

	out := &Spec{Services: make([]Service, 0, len(raw.Services))}
	for name, rs := range raw.Services {
		out.Services = append(out.Services, Service{
			Name:           name,
			Image:          strings.TrimSpace(rs.Image),
			HasBuild:       !rs.Build.IsZero(),
			Build:          decodeBuild(rs.Build),
			Ports:          collectPorts(rs.Ports, rs.Expose),
			HasHealthcheck: !rs.Healthcheck.IsZero(),
			Env:            decodeEnv(rs.Environment),
			Command:        decodeCommand(rs.Command),
			Restart:        strings.TrimSpace(rs.Restart),
			DependsOn:      decodeDependsOn(rs.DependsOn),
		})
	}
	return out, nil
}

// decodeBuild normalises the two `build:` syntaxes:
//
//	build: .                          # short — string is the context
//	build:                            # long  — explicit fields
//	  context: ./backend
//	  dockerfile: Dockerfile.prod
//	  args:
//	    NODE_ENV: production
//	    API_URL: https://api.example.com
//
// Anything missing is filled with sensible defaults at build-time
// (context = ".", dockerfile = "Dockerfile") — we don't fill them
// here so the caller can distinguish "operator set it" from "default".
func decodeBuild(n yaml.Node) *BuildSpec {
	if n.IsZero() {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		// Short form — just the context path.
		v := strings.TrimSpace(n.Value)
		if v == "" {
			return nil
		}
		return &BuildSpec{Context: v}
	case yaml.MappingNode:
		var raw struct {
			Context    string    `yaml:"context"`
			Dockerfile string    `yaml:"dockerfile"`
			Args       yaml.Node `yaml:"args"`
		}
		if err := n.Decode(&raw); err != nil {
			return nil
		}
		return &BuildSpec{
			Context:    strings.TrimSpace(raw.Context),
			Dockerfile: strings.TrimSpace(raw.Dockerfile),
			// Args reuses the env normaliser — same list-of-strings
			// vs. map dual syntax as `environment:`.
			Args: decodeEnv(raw.Args),
		}
	}
	return nil
}

// decodeEnv normalises the two `environment:` syntaxes (list of
// "KEY=VALUE" strings or a map of KEY: VALUE) into a single map.
// Compose also allows bare "KEY" entries that inherit from the
// process environment — Prexel doesn't have that concept (we run
// in a sandboxed container), so bare keys collapse to empty values.
func decodeEnv(n yaml.Node) map[string]string {
	if n.IsZero() {
		return nil
	}
	out := map[string]string{}
	switch n.Kind {
	case yaml.MappingNode:
		// `environment: { KEY: VALUE, ... }` form. yaml.Node decodes
		// into map[string]string when values are scalar strings, but
		// non-string scalars (numbers, bools) also need to flatten.
		var m map[string]string
		if err := n.Decode(&m); err == nil {
			for k, v := range m {
				out[k] = v
			}
		}
	case yaml.SequenceNode:
		// `environment: ["KEY=VALUE", "OTHER=value"]` form.
		var list []string
		if err := n.Decode(&list); err == nil {
			for _, item := range list {
				k, v, ok := strings.Cut(item, "=")
				if !ok {
					out[strings.TrimSpace(k)] = ""
					continue
				}
				out[strings.TrimSpace(k)] = v
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// decodeCommand normalises the two `command:` syntaxes (single
// string or list of args) into a string slice. The string form is
// split on spaces — naive but matches typical usage; quoting tricks
// would require a full shell parse we deliberately don't ship.
func decodeCommand(n yaml.Node) []string {
	if n.IsZero() {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		s := strings.TrimSpace(n.Value)
		if s == "" {
			return nil
		}
		return strings.Fields(s)
	case yaml.SequenceNode:
		var out []string
		if err := n.Decode(&out); err == nil {
			return out
		}
	}
	return nil
}

// decodeDependsOn normalises the two `depends_on:` syntaxes:
//   - short: `depends_on: [db, redis]`
//   - long:  `depends_on: { db: { condition: service_healthy } }`
//
// We only care about the service names; the `condition:` field is
// out of scope for now (we don't model health-based startup gating).
func decodeDependsOn(n yaml.Node) []string {
	if n.IsZero() {
		return nil
	}
	switch n.Kind {
	case yaml.SequenceNode:
		var out []string
		if err := n.Decode(&out); err == nil {
			return out
		}
	case yaml.MappingNode:
		var m map[string]yaml.Node
		if err := n.Decode(&m); err != nil {
			return nil
		}
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		return out
	}
	return nil
}

// collectPorts walks `ports:` and `expose:` entries, normalising both
// shapes (short syntax "8080:80" / long object form) into a unique
// container-port list. Host port and protocol are intentionally
// dropped — the runner cares about that, the UI doesn't.
func collectPorts(ports, expose []yaml.Node) []int {
	seen := map[int]struct{}{}
	out := []int{}
	add := func(p int) {
		if p <= 0 || p > 65535 {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}

	for _, n := range ports {
		switch n.Kind {
		case yaml.ScalarNode:
			// "8080:80" / "80" / "127.0.0.1:8080:80"
			add(extractContainerPort(n.Value))
		case yaml.MappingNode:
			// Long form: { target: 80, published: 8080, protocol: tcp }
			var m struct {
				Target int `yaml:"target"`
			}
			if err := n.Decode(&m); err == nil {
				add(m.Target)
			}
		}
	}
	for _, n := range expose {
		if n.Kind == yaml.ScalarNode {
			add(extractContainerPort(n.Value))
		}
	}
	return out
}

// extractContainerPort isolates the target port from short-syntax
// strings: "80", "8080:80", "127.0.0.1:8080:80", "8080:80/udp".
// Returns 0 when it can't parse — caller filters those out.
func extractContainerPort(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// Drop protocol suffix.
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	// Container port is always the LAST colon-separated segment.
	parts := strings.Split(s, ":")
	last := parts[len(parts)-1]
	// Range syntax "8080-8082" — pick the first end.
	if i := strings.Index(last, "-"); i >= 0 {
		last = last[:i]
	}
	n, err := strconv.Atoi(strings.TrimSpace(last))
	if err != nil {
		return 0
	}
	return n
}
