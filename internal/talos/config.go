package talos

import (
	"encoding/base64"
	"fmt"

	"gopkg.in/yaml.v3"
)

// PatchHostname sets machine.network.hostname in a Talos machine config YAML.
// Uses generic map manipulation to avoid depending on Talos Go types.
func PatchHostname(configYAML []byte, hostname string) ([]byte, error) {
	var config map[string]interface{}
	if err := yaml.Unmarshal(configYAML, &config); err != nil {
		return nil, fmt.Errorf("unmarshaling machine config: %w", err)
	}

	machine, ok := config["machine"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("machine config missing 'machine' key")
	}

	network, ok := machine["network"].(map[string]interface{})
	if !ok {
		network = make(map[string]interface{})
		machine["network"] = network
	}

	network["hostname"] = hostname

	patched, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshaling patched config: %w", err)
	}
	return patched, nil
}

// PatchAndEncode patches the hostname and returns base64-encoded userdata.
func PatchAndEncode(configYAML []byte, hostname string) (string, error) {
	patched, err := PatchHostname(configYAML, hostname)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(patched), nil
}
