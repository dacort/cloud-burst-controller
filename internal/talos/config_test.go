package talos

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const sampleConfig = `machine:
  type: worker
  network:
    hostname: ""
    kubespan:
      enabled: true
  install:
    disk: /dev/xvda
cluster:
  controlPlane:
    endpoint: https://192.168.7.199:6443
`

func TestPatchHostname(t *testing.T) {
	patched, err := PatchHostname([]byte(sampleConfig), "burst-general-abc123")
	require.NoError(t, err)

	// Verify hostname was set
	assert.Contains(t, string(patched), "hostname: burst-general-abc123")
	// Verify other fields preserved
	assert.Contains(t, string(patched), "kubespan:")
	assert.Contains(t, string(patched), "enabled: true")
	assert.Contains(t, string(patched), "endpoint: https://192.168.7.199:6443")
}

func TestPatchHostname_InvalidYAML(t *testing.T) {
	_, err := PatchHostname([]byte("not: valid: yaml: ["), "test")
	assert.Error(t, err)
}

func TestPatchHostname_MissingMachineKey(t *testing.T) {
	_, err := PatchHostname([]byte("cluster:\n  name: test\n"), "test")
	assert.Error(t, err)
}

func TestPatchAndEncode(t *testing.T) {
	encoded, err := PatchAndEncode([]byte(sampleConfig), "burst-node-1", nil)
	require.NoError(t, err)

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)

	// JSON, which Talos reads as YAML: the same config, hostname set.
	var got map[string]any
	require.NoError(t, json.Unmarshal(decoded, &got))
	var asYAML map[string]any
	require.NoError(t, yaml.Unmarshal(decoded, &asYAML))
	assert.Equal(t, got, asYAML)
	machine := got["machine"].(map[string]any)
	assert.Equal(t, "burst-node-1", machine["network"].(map[string]any)["hostname"])
	assert.Equal(t, true, machine["network"].(map[string]any)["kubespan"].(map[string]any)["enabled"])
	assert.Equal(t, "https://192.168.7.199:6443", got["cluster"].(map[string]any)["controlPlane"].(map[string]any)["endpoint"])
}

// A seccomp profile in the config (testdata: Docker's default profile,
// reduced to a no-capabilities container, plus one unshare rule) is many small
// lists and maps, which block-style YAML spreads over many lines. As JSON
// it stays near its written size, under EC2's user data limit.
func TestPatchAndEncode_SeccompProfileFits(t *testing.T) {
	raw, err := os.ReadFile("testdata/seccomp-profile.json")
	require.NoError(t, err)
	var profile map[string]any
	// Through YAML, as the controller reads it, so numbers compare as ints.
	require.NoError(t, yaml.Unmarshal(raw, &profile))
	var config map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(sampleConfig), &config))
	config["machine"].(map[string]any)["seccompProfiles"] = []any{map[string]any{"name": "custom-profile.json", "value": profile}}
	cfg, err := yaml.Marshal(config)
	require.NoError(t, err)

	asYAML, err := PatchHostname(cfg, "burst-node-1")
	require.NoError(t, err)
	require.Greater(t, len(base64.StdEncoding.EncodeToString(asYAML)), MaxUserDataBytes, "the fixture should be too big as YAML")

	encoded, err := PatchAndEncode(cfg, "burst-node-1", nil)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(encoded), MaxUserDataBytes)

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, yaml.Unmarshal(decoded, &got))
	assert.Equal(t, profile, got["machine"].(map[string]any)["seccompProfiles"].([]any)[0].(map[string]any)["value"])
}

func TestPatchAndEncode_TooBig(t *testing.T) {
	cfg := sampleConfig + "  padding: " + strings.Repeat("x", MaxUserDataBytes) + "\n"
	cfg = strings.Replace(cfg, "cluster:\n  controlPlane:\n    endpoint: https://192.168.7.199:6443\n", "", 1)
	_, err := PatchAndEncode([]byte(cfg), "burst-node-1", nil)
	assert.ErrorContains(t, err, "over EC2's limit")
}

// Taints go into machine.nodeTaints, next to any the config already has,
// so the node registers with them.
func TestPatchAndEncode_NodeTaints(t *testing.T) {
	cfg := strings.Replace(sampleConfig, "machine:\n", "machine:\n  nodeTaints:\n    existing: \"yes:NoExecute\"\n", 1)
	encoded, err := PatchAndEncode([]byte(cfg), "burst-node-1", map[string]string{
		"burst.homelab.dev/cloud": TaintValue("true", "NoSchedule"),
	})
	require.NoError(t, err)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(decoded, &got))
	assert.Equal(t, map[string]any{
		"existing":                "yes:NoExecute",
		"burst.homelab.dev/cloud": "true:NoSchedule",
	}, got["machine"].(map[string]any)["nodeTaints"])
}
