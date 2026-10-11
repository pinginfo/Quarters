// Package resolve merges a TenantProfile with the overrides of a Tenant.
package resolve

import (
	"encoding/json"
	"fmt"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

// Merge returns profile overlaid with overrides. Maps are merged key by key,
// lists and scalars in overrides replace the profile value. Neither input is
// modified. A nil profile is treated as empty.
func Merge(profile *quartersv1alpha1.TenantConfig, overrides quartersv1alpha1.TenantConfig) (quartersv1alpha1.TenantConfig, error) {
	base, err := toMap(profile)
	if err != nil {
		return quartersv1alpha1.TenantConfig{}, fmt.Errorf("profile: %w", err)
	}
	over, err := toMap(&overrides)
	if err != nil {
		return quartersv1alpha1.TenantConfig{}, fmt.Errorf("tenant: %w", err)
	}
	merged := mergeMaps(base, over)

	raw, err := json.Marshal(merged)
	if err != nil {
		return quartersv1alpha1.TenantConfig{}, err
	}
	var out quartersv1alpha1.TenantConfig
	if err := json.Unmarshal(raw, &out); err != nil {
		return quartersv1alpha1.TenantConfig{}, fmt.Errorf("merged config: %w", err)
	}
	return out, nil
}

func toMap(c *quartersv1alpha1.TenantConfig) (map[string]any, error) {
	m := map[string]any{}
	if c == nil {
		return m, nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func mergeMaps(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		bm, bok := out[k].(map[string]any)
		om, ook := v.(map[string]any)
		if bok && ook {
			out[k] = mergeMaps(bm, om)
			continue
		}
		out[k] = v
	}
	return out
}
