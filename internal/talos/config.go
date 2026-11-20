package talos

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// PatchHostname sets machine.network.hostname in a Talos machine config YAML.
// Uses generic map manipulation to avoid depending on Talos Go types.
func PatchHostname(configYAML []byte, hostname string) ([]byte, error) {
	config, err := patchedConfig(configYAML, hostname, nil)
	if err != nil {
		return nil, err
	}
	patched, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshaling patched config: %w", err)
	}
	return patched, nil
}

// patchedConfig parses the first document of a machine config, sets
// machine.network.hostname in it, and adds taints to machine.nodeTaints.
func patchedConfig(configYAML []byte, hostname string, taints map[string]string) (map[string]any, error) {
	var config map[string]any
	if err := yaml.Unmarshal(configYAML, &config); err != nil {
		return nil, fmt.Errorf("unmarshaling machine config: %w", err)
	}

	machine, ok := config["machine"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("machine config missing 'machine' key")
	}

	network, ok := machine["network"].(map[string]any)
	if !ok {
		network = make(map[string]any)
		machine["network"] = network
	}

	network["hostname"] = hostname

	if len(taints) > 0 {
		nodeTaints, ok := machine["nodeTaints"].(map[string]any)
		if !ok {
			nodeTaints = make(map[string]any)
			machine["nodeTaints"] = nodeTaints
		}
		for key, valueEffect := range taints {
			nodeTaints[key] = valueEffect
		}
	}
	return config, nil
}

// MaxUserDataBytes is EC2's limit on base64-encoded user data.
const MaxUserDataBytes = 25600

// PatchAndEncode patches the hostname and node taints and returns
// base64-encoded userdata. taints maps each taint key to Talos's
// "value:Effect" form (see TaintValue); Talos puts them on the node when it
// registers, before anything can be scheduled there.
//
// The config goes out as compact JSON, which Talos reads as YAML, not as
// re-marshaled YAML: block-style YAML expands nested lists and maps (a
// seccomp profile in machine.seccompProfiles, say) to several times their
// size, past EC2's 25,600-byte user data limit. A config still too big is
// an error here rather than a RunInstances rejection.
func PatchAndEncode(configYAML []byte, hostname string, taints map[string]string) (string, error) {
	config, err := patchedConfig(configYAML, hostname, taints)
	if err != nil {
		return "", err
	}
	patched, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshaling patched config: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(patched)
	if len(encoded) > MaxUserDataBytes {
		return "", fmt.Errorf("machine config is %d bytes as base64 user data, over EC2's limit of %d", len(encoded), MaxUserDataBytes)
	}
	return encoded, nil
}

// TaintValue is a taint's value and effect in the "value:Effect" form
// machine.nodeTaints takes.
func TaintValue(value, effect string) string {
	return value + ":" + effect
}
