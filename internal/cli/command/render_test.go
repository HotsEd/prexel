package command

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// updateGolden returns true when PREXEL_UPDATE_GOLDEN=1, used to regenerate
// golden files. We avoid a flag here because Cobra commands invoked from tests
// (TestVersionCommand) re-parse their own arguments and would choke on an
// unknown -update flag.
func updateGolden() bool { return os.Getenv("PREXEL_UPDATE_GOLDEN") == "1" }

func loadGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if updateGolden() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %q: %v (run with -update to create)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func ptr[T any](v T) *T { return &v }

func TestRenderServerList_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderServerList(&buf, nil, nil); err != nil {
		t.Fatalf("render: %v", err)
	}
	loadGolden(t, "server_list_empty.txt", buf.Bytes())
}

func TestRenderServerList_TwoServers(t *testing.T) {
	servers := []serverDTO{
		{
			ID:            "srv-1",
			Name:          "local",
			Type:          "local",
			Port:          22,
			Status:        "connected",
			DockerVersion: ptr("27.0.3"),
		},
		{
			ID:            "srv-2",
			Name:          "prod-eu",
			Type:          "remote",
			Host:          ptr("10.0.0.1"),
			Port:          22,
			User:          ptr("root"),
			Status:        "disconnected",
			DockerVersion: nil,
		},
	}
	counts := map[string]int{"srv-1": 2, "srv-2": 0}

	var buf bytes.Buffer
	if err := renderServerList(&buf, servers, counts); err != nil {
		t.Fatalf("render: %v", err)
	}
	loadGolden(t, "server_list_two.txt", buf.Bytes())
}

func TestRenderAppList_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderAppList(&buf, nil, nil, nil, nil); err != nil {
		t.Fatalf("render: %v", err)
	}
	loadGolden(t, "app_list_empty.txt", buf.Bytes())
}

func TestRenderAppList_TwoApps(t *testing.T) {
	apps := []appDTO{
		{
			ID:        "app-1",
			Name:      "web",
			ServerID:  ptr("srv-1"),
			BuildType: "dockerfile",
			Status:    "running",
		},
		{
			ID:        "app-2",
			Name:      "worker",
			ServerID:  ptr("srv-2"),
			BuildType: "docker_image",
			Status:    "idle",
		},
	}
	serverNames := map[string]string{"srv-1": "local", "srv-2": "prod-eu"}
	domains := map[string]string{"app-1": "web.example.com"}
	deploys := map[string]string{"app-1": "success (2026-05-11 12:00Z)"}

	var buf bytes.Buffer
	if err := renderAppList(&buf, apps, serverNames, domains, deploys); err != nil {
		t.Fatalf("render: %v", err)
	}
	loadGolden(t, "app_list_two.txt", buf.Bytes())
}

func TestVersionCommand(t *testing.T) {
	cmd := newVersionCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	loadGolden(t, "version.txt", buf.Bytes())
}
