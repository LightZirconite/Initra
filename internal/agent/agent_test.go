package agent

import (
	"strings"
	"testing"
)

func TestSelectAgentArtifact(t *testing.T) {
	manifest := manifestResponse{
		Artifacts: map[string]string{
			"agent-windows-amd64": "https://example.test/agent.exe",
			"agent-linux-amd64":   "https://example.test/agent",
		},
		Sha256: map[string]string{
			"agent-windows-amd64": "abc",
			"agent-linux-amd64":   "def",
		},
	}

	artifact, ok := SelectAgentArtifact(manifest, "windows", "amd64")
	if !ok {
		t.Fatalf("expected windows artifact")
	}
	if artifact.Key != "agent-windows-amd64" || artifact.URL == "" || artifact.SHA256 != "abc" {
		t.Fatalf("unexpected windows artifact: %+v", artifact)
	}

	artifact, ok = SelectAgentArtifact(manifest, "linux", "amd64")
	if !ok {
		t.Fatalf("expected linux artifact")
	}
	if artifact.Key != "agent-linux-amd64" || artifact.SHA256 != "def" {
		t.Fatalf("unexpected linux artifact: %+v", artifact)
	}

	if _, ok := SelectAgentArtifact(manifest, "darwin", "amd64"); ok {
		t.Fatalf("did not expect darwin artifact")
	}
}

func TestDecodeJSONBodyAllowsUTF8BOM(t *testing.T) {
	var payload manifestResponse
	if err := decodeJSONBody(strings.NewReader("\ufeff{\"version\":\"ok\"}"), &payload); err != nil {
		t.Fatalf("decodeJSONBody() error = %v", err)
	}
	if payload.Version != "ok" {
		t.Fatalf("unexpected version: %s", payload.Version)
	}
}
