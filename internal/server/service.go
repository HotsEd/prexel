package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/crypto"
	"github.com/prexel/prexel/internal/dockersvc"
	prexelssh "github.com/prexel/prexel/internal/ssh"
)

// checkTimeout is the per-step ceiling used by TestConnection (Spec: Servers).
const checkTimeout = 10 * time.Second

// Service is the domain entry point for the servers table.
type Service struct {
	repo   *repo
	cipher *crypto.Cipher
}

// NewService wires a Service. The cipher is used to encrypt private SSH keys
// at rest and decrypt them under demand.
func NewService(db *sql.DB, cipher *crypto.Cipher) *Service {
	return &Service{repo: newRepo(db), cipher: cipher}
}

// CreateInput is the union of fields accepted by Create. Validation depends on
// Type:
//
//	type=local:  Name required, everything else ignored.
//	type=remote: Name + Host + User required, Port defaults to 22.
//	             Either GenerateKey or PrivateKey must be supplied.
type CreateInput struct {
	Type        string // "local" | "remote"
	Name        string
	Host        string
	Port        int
	User        string
	GenerateKey bool
	PrivateKey  string // PEM-encoded
}

// Patch is the partial-update payload for UpdateInput. Type is intentionally
// absent — switching local<->remote is not supported.
type Patch struct {
	Name *string
	Host *string
	Port *int
	User *string
}

// Create inserts a new server. Behaviour differs between local and remote
// (see CreateInput).
func (s *Service) Create(ctx context.Context, in CreateInput) (*Server, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, errors.New("missing_name")
	}

	switch in.Type {
	case "local":
		return s.createLocal(ctx, name)
	case "remote":
		return s.createRemote(ctx, name, in)
	default:
		return nil, fmt.Errorf("invalid_type: %q", in.Type)
	}
}

func (s *Service) createLocal(ctx context.Context, name string) (*Server, error) {
	n, err := s.repo.countLocal(ctx)
	if err != nil {
		return nil, fmt.Errorf("count local: %w", err)
	}
	if n > 0 {
		return nil, ErrLocalAlreadyExists
	}

	// Probe Docker now so the create returns useful state. A failure here
	// surfaces as a 400 to the caller (handler maps it).
	p := dockersvc.NewLocalProvider(name)
	defer func() { _ = p.Close() }()

	probeCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	if _, err := dockersvc.Info(probeCtx, p); err != nil {
		return nil, fmt.Errorf("docker unreachable: %w", err)
	}
	v, err := dockersvc.Version(probeCtx, p)
	if err != nil {
		// Could be ErrUnsupportedDockerVersion — propagate so the handler can map.
		return nil, fmt.Errorf("docker version: %w", err)
	}

	now := time.Now().Unix()
	srv := &Server{
		ID:            uuid.NewString(),
		Name:          name,
		Type:          "local",
		Port:          22,
		Status:        "connected",
		DockerVersion: strPtr(v.Version),
		LastCheckedAt: &now,
	}
	if err := s.repo.insert(ctx, srv, nil); err != nil {
		return nil, err
	}
	return s.repo.get(ctx, srv.ID)
}

func (s *Service) createRemote(ctx context.Context, name string, in CreateInput) (*Server, error) {
	host := strings.TrimSpace(in.Host)
	user := strings.TrimSpace(in.User)
	port := in.Port
	if port == 0 {
		port = 22
	}
	if host == "" || user == "" {
		return nil, errors.New("missing_fields")
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid_port: %d", port)
	}

	var privatePEM []byte
	var publicSSH string
	switch {
	case in.GenerateKey:
		pem, pub, err := prexelssh.GenerateEd25519()
		if err != nil {
			return nil, fmt.Errorf("generate key: %w", err)
		}
		privatePEM = pem
		publicSSH = strings.TrimSpace(pub)
	case in.PrivateKey != "":
		raw := []byte(in.PrivateKey)
		signer, err := prexelssh.ParseKey(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid_private_key: %w", err)
		}
		privatePEM = raw
		publicSSH = prexelssh.PublicKeyToAuthorizedKey(signer)
	default:
		return nil, errors.New("missing_key: pass generate_key=true or private_key")
	}

	encrypted, err := s.cipher.Encrypt(privatePEM)
	if err != nil {
		return nil, fmt.Errorf("encrypt key: %w", err)
	}

	srv := &Server{
		ID:     uuid.NewString(),
		Name:   name,
		Type:   "remote",
		Host:   &host,
		Port:   port,
		User:   &user,
		Status: "unknown",
	}
	if err := s.repo.insert(ctx, srv, encrypted); err != nil {
		return nil, err
	}
	got, err := s.repo.get(ctx, srv.ID)
	if err != nil {
		return nil, err
	}
	got.PublicKey = publicSSH
	return got, nil
}

// List returns every server (no private key data).
func (s *Service) List(ctx context.Context) ([]Server, error) {
	return s.repo.list(ctx)
}

// Get returns a single server by id.
func (s *Service) Get(ctx context.Context, id string) (*Server, error) {
	return s.repo.get(ctx, id)
}

// Update applies a partial update.
func (s *Service) Update(ctx context.Context, id string, p Patch) (*Server, error) {
	if p.Port != nil {
		if *p.Port < 1 || *p.Port > 65535 {
			return nil, fmt.Errorf("invalid_port: %d", *p.Port)
		}
	}
	if p.Name != nil {
		trimmed := strings.TrimSpace(*p.Name)
		if trimmed == "" {
			return nil, errors.New("invalid_name")
		}
		p.Name = &trimmed
	}
	return s.repo.updatePatch(ctx, id, p)
}

// Delete removes a server, blocking when apps still reference it.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.delete(ctx, id)
}

// Check is one row in the TestReport returned by TestConnection.
type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// TestReport aggregates the individual checks for a server.
type TestReport struct {
	ServerID string  `json:"server_id"`
	Type     string  `json:"type"`
	Status   string  `json:"status"`
	Checks   []Check `json:"checks"`
}

// TestConnection runs the full diagnostic battery against a server, updates the
// servers row (status, docker_version, last_checked_at), and returns the
// per-check report. The function never returns an error for individual check
// failures — those land in the report. A returned error means the call itself
// could not be staged (e.g. the server id is unknown).
func (s *Service) TestConnection(ctx context.Context, id string) (*TestReport, error) {
	srv, err := s.repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if srv.Type == "local" {
		return s.testLocal(ctx, srv)
	}
	return s.testRemote(ctx, srv)
}

func (s *Service) testLocal(ctx context.Context, srv *Server) (*TestReport, error) {
	report := &TestReport{ServerID: srv.ID, Type: "local"}
	p := dockersvc.NewLocalProvider(srv.Name)
	defer func() { _ = p.Close() }()

	infoCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	infoChk := Check{Name: "docker_info"}
	if _, err := dockersvc.Info(infoCtx, p); err != nil {
		infoChk.OK = false
		infoChk.Message = fmt.Sprintf("docker info failed: %v", err)
		report.Checks = append(report.Checks, infoChk)
		_ = s.repo.updateStatus(ctx, srv.ID, "disconnected", nil)
		report.Status = "disconnected"
		return report, nil
	}
	infoChk.OK = true
	infoChk.Message = "docker daemon reachable"
	report.Checks = append(report.Checks, infoChk)

	verChk := Check{Name: "docker_version"}
	vCtx, cancel2 := context.WithTimeout(ctx, checkTimeout)
	defer cancel2()
	v, err := dockersvc.Version(vCtx, p)
	if err != nil {
		verChk.OK = false
		verChk.Message = err.Error()
		report.Checks = append(report.Checks, verChk)
		_ = s.repo.updateStatus(ctx, srv.ID, "disconnected", strPtrOrNil(v.Version))
		report.Status = "disconnected"
		return report, nil
	}
	verChk.OK = true
	verChk.Message = fmt.Sprintf("Docker %s (API %s)", v.Version, v.APIVersion)
	report.Checks = append(report.Checks, verChk)

	// Docker socket permission — implicit (Info+Version succeeded). Record it
	// so the UI checklist matches the Spec.
	report.Checks = append(report.Checks, Check{
		Name: "docker_socket", OK: true, Message: "socket accessible",
	})

	_ = s.repo.updateStatus(ctx, srv.ID, "connected", strPtr(v.Version))
	report.Status = "connected"
	return report, nil
}

func (s *Service) testRemote(ctx context.Context, srv *Server) (*TestReport, error) {
	report := &TestReport{ServerID: srv.ID, Type: "remote"}
	if srv.Host == nil || srv.User == nil {
		report.Checks = append(report.Checks, Check{Name: "config", OK: false, Message: "missing host/user"})
		report.Status = "disconnected"
		_ = s.repo.updateStatus(ctx, srv.ID, "disconnected", nil)
		return report, nil
	}
	host := *srv.Host
	user := *srv.User

	// 1. DNS / hostname resolves.
	dnsChk := Check{Name: "hostname_resolves"}
	resolveCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	resolver := net.DefaultResolver
	if _, err := resolver.LookupHost(resolveCtx, host); err != nil {
		dnsChk.OK = false
		dnsChk.Message = fmt.Sprintf("DNS lookup failed: %v", err)
		report.Checks = append(report.Checks, dnsChk)
		_ = s.repo.updateStatus(ctx, srv.ID, "disconnected", nil)
		report.Status = "disconnected"
		return report, nil
	}
	dnsChk.OK = true
	dnsChk.Message = "hostname resolves"
	report.Checks = append(report.Checks, dnsChk)

	// 2. TCP reachability on the SSH port.
	tcpChk := Check{Name: "ssh_port_open"}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", srv.Port))
	dialCtx, cancel2 := context.WithTimeout(ctx, checkTimeout)
	defer cancel2()
	dialer := &net.Dialer{Timeout: checkTimeout}
	conn, err := dialer.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		tcpChk.OK = false
		tcpChk.Message = fmt.Sprintf("cannot reach %s: %v", addr, err)
		report.Checks = append(report.Checks, tcpChk)
		_ = s.repo.updateStatus(ctx, srv.ID, "disconnected", nil)
		report.Status = "disconnected"
		return report, nil
	}
	_ = conn.Close()
	tcpChk.OK = true
	tcpChk.Message = fmt.Sprintf("connected to %s", addr)
	report.Checks = append(report.Checks, tcpChk)

	// 3. SSH authentication (and host-key capture/verification).
	authChk := Check{Name: "ssh_authenticates"}
	encryptedKey, err := s.repo.getPrivateKey(ctx, srv.ID)
	if err != nil || len(encryptedKey) == 0 {
		authChk.OK = false
		authChk.Message = "no private key stored"
		report.Checks = append(report.Checks, authChk)
		_ = s.repo.updateStatus(ctx, srv.ID, "disconnected", nil)
		report.Status = "disconnected"
		return report, nil
	}
	privatePEM, err := s.cipher.Decrypt(encryptedKey)
	if err != nil {
		authChk.OK = false
		authChk.Message = fmt.Sprintf("decrypt private key: %v", err)
		report.Checks = append(report.Checks, authChk)
		_ = s.repo.updateStatus(ctx, srv.ID, "disconnected", nil)
		report.Status = "disconnected"
		return report, nil
	}
	signer, err := prexelssh.ParseKey(privatePEM)
	if err != nil {
		authChk.OK = false
		authChk.Message = fmt.Sprintf("parse private key: %v", err)
		report.Checks = append(report.Checks, authChk)
		_ = s.repo.updateStatus(ctx, srv.ID, "disconnected", nil)
		report.Status = "disconnected"
		return report, nil
	}

	storedFP := ""
	if srv.HostKeyFingerprint != nil {
		storedFP = *srv.HostKeyFingerprint
	}
	hk := prexelssh.Callback(storedFP, func(fp string) error {
		// Persist on first contact. Errors here propagate up the SSH handshake.
		return s.repo.updateHostKey(ctx, srv.ID, fp)
	})

	sshCtx, cancel3 := context.WithTimeout(ctx, checkTimeout)
	defer cancel3()
	sshClient, err := prexelssh.Connect(sshCtx, host, srv.Port, user, signer, hk, checkTimeout)
	if err != nil {
		authChk.OK = false
		if errors.Is(err, prexelssh.ErrHostKeyMismatch) {
			authChk.Message = fmt.Sprintf("host key mismatch — refusing to connect: %v", err)
		} else {
			authChk.Message = fmt.Sprintf("ssh handshake failed: %v", err)
		}
		report.Checks = append(report.Checks, authChk)
		_ = s.repo.updateStatus(ctx, srv.ID, "disconnected", nil)
		report.Status = "disconnected"
		return report, nil
	}
	defer func() { _ = sshClient.Close() }()
	authChk.OK = true
	authChk.Message = fmt.Sprintf("authenticated as %s", user)
	report.Checks = append(report.Checks, authChk)

	// 4. Docker reachable on the remote (via the docker remote provider, which
	// uses connhelper SSH internally — same keypath at runtime, but here we
	// re-write the decrypted PEM into a temp file managed by the provider).
	dockerChk := Check{Name: "docker_info"}
	rp, err := dockersvc.NewRemoteProvider(srv.Name, host, srv.Port, user, privatePEM)
	if err != nil {
		dockerChk.OK = false
		dockerChk.Message = fmt.Sprintf("build provider: %v", err)
		report.Checks = append(report.Checks, dockerChk)
		_ = s.repo.updateStatus(ctx, srv.ID, "disconnected", nil)
		report.Status = "disconnected"
		return report, nil
	}
	defer func() { _ = rp.Close() }()

	dCtx, cancel4 := context.WithTimeout(ctx, checkTimeout)
	defer cancel4()
	if _, err := dockersvc.Info(dCtx, rp); err != nil {
		dockerChk.OK = false
		dockerChk.Message = fmt.Sprintf("docker info failed: %v", err)
		report.Checks = append(report.Checks, dockerChk)
		_ = s.repo.updateStatus(ctx, srv.ID, "disconnected", nil)
		report.Status = "disconnected"
		return report, nil
	}
	dockerChk.OK = true
	dockerChk.Message = "docker daemon reachable via SSH"
	report.Checks = append(report.Checks, dockerChk)

	// 5. Docker version >= 20.10.
	verChk := Check{Name: "docker_version"}
	vCtx, cancel5 := context.WithTimeout(ctx, checkTimeout)
	defer cancel5()
	v, err := dockersvc.Version(vCtx, rp)
	if err != nil {
		verChk.OK = false
		verChk.Message = err.Error()
		report.Checks = append(report.Checks, verChk)
		_ = s.repo.updateStatus(ctx, srv.ID, "disconnected", strPtrOrNil(v.Version))
		report.Status = "disconnected"
		return report, nil
	}
	verChk.OK = true
	verChk.Message = fmt.Sprintf("Docker %s (API %s)", v.Version, v.APIVersion)
	report.Checks = append(report.Checks, verChk)

	// 6. Socket access — implied by successful Info.
	report.Checks = append(report.Checks, Check{
		Name: "docker_socket", OK: true, Message: "remote socket accessible",
	})

	_ = s.repo.updateStatus(ctx, srv.ID, "connected", strPtr(v.Version))
	report.Status = "connected"
	return report, nil
}

// ping is the lightweight health check used by the status loop. It returns
// (status, dockerVersion, error). It doesn't persist anything — the caller
// decides whether/how to update state.
func (s *Service) ping(ctx context.Context, srv *Server) (string, *string, error) {
	switch srv.Type {
	case "local":
		p := dockersvc.NewLocalProvider(srv.Name)
		defer func() { _ = p.Close() }()
		pingCtx, cancel := context.WithTimeout(ctx, checkTimeout)
		defer cancel()
		v, err := dockersvc.Version(pingCtx, p)
		if err != nil {
			return "disconnected", nil, err
		}
		return "connected", strPtr(v.Version), nil
	case "remote":
		if srv.Host == nil || srv.User == nil {
			return "disconnected", nil, errors.New("missing host/user")
		}
		encrypted, err := s.repo.getPrivateKey(ctx, srv.ID)
		if err != nil || len(encrypted) == 0 {
			return "unknown", nil, errors.New("no private key")
		}
		privatePEM, err := s.cipher.Decrypt(encrypted)
		if err != nil {
			return "disconnected", nil, err
		}
		rp, err := dockersvc.NewRemoteProvider(srv.Name, *srv.Host, srv.Port, *srv.User, privatePEM)
		if err != nil {
			return "disconnected", nil, err
		}
		defer func() { _ = rp.Close() }()
		pingCtx, cancel := context.WithTimeout(ctx, checkTimeout)
		defer cancel()
		v, err := dockersvc.Version(pingCtx, rp)
		if err != nil {
			return "disconnected", nil, err
		}
		return "connected", strPtr(v.Version), nil
	}
	return "unknown", nil, nil
}

// Provider builds a dockersvc.Provider for the given server id. The caller is
// responsible for Close()ing the returned provider when done. Remote providers
// decrypt the SSH private key on the fly; the decrypted key never leaves the
// caller's process (it is written to a chmod-600 temp file by the provider
// and removed on Close).
func (s *Service) Provider(ctx context.Context, id string) (dockersvc.Provider, error) {
	srv, err := s.repo.get(ctx, id)
	if err != nil {
		return nil, err
	}
	switch srv.Type {
	case "local":
		return dockersvc.NewLocalProvider(srv.Name), nil
	case "remote":
		if srv.Host == nil || srv.User == nil {
			return nil, errors.New("server: missing host/user")
		}
		encrypted, err := s.repo.getPrivateKey(ctx, srv.ID)
		if err != nil {
			return nil, fmt.Errorf("server: load key: %w", err)
		}
		if len(encrypted) == 0 {
			return nil, errors.New("server: no private key stored")
		}
		privatePEM, err := s.cipher.Decrypt(encrypted)
		if err != nil {
			return nil, fmt.Errorf("server: decrypt key: %w", err)
		}
		return dockersvc.NewRemoteProvider(srv.Name, *srv.Host, srv.Port, *srv.User, privatePEM)
	}
	return nil, fmt.Errorf("server: unknown type %q", srv.Type)
}

func strPtr(s string) *string { return &s }

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
