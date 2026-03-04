package talos

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	encoded, err := PatchAndEncode([]byte(sampleConfig), "burst-node-1")
	require.NoError(t, err)

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	assert.Contains(t, string(decoded), "hostname: burst-node-1")
}
