// Copyright 2025 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package context

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openchoreo/openchoreo/internal/crd-renderer/schema"
)

// BuildAddonContext builds a CEL evaluation context for rendering addon resources.
//
// The context includes:
//   - parameters: Addon instance parameters with environment overrides and schema defaults applied
//   - addon: Addon metadata (name, instanceId)
//   - component: Component reference (name, etc.)
//   - environment: Environment name
//   - metadata: Additional metadata
//
// Parameter precedence (highest to lowest):
//  1. EnvSettings.Spec.AddonOverrides[addonName][instanceId] (environment-specific)
//  2. AddonInstance.Parameters (instance parameters)
//  3. Schema defaults from Addon
func BuildAddonContext(input *AddonContextInput) (map[string]any, error) {
	if input == nil {
		return nil, fmt.Errorf("addon context input is nil")
	}
	if input.Addon == nil {
		return nil, fmt.Errorf("addon is nil")
	}
	if input.Component == nil {
		return nil, fmt.Errorf("component is nil")
	}

	ctx := make(map[string]any)

	// 1. Build and apply schema for defaulting
	schemaInput := &SchemaInput{
		ParametersSchema:   input.Addon.Spec.Schema.Parameters,
		EnvOverridesSchema: input.Addon.Spec.Schema.EnvOverrides,
	}
	structural, err := buildStructuralSchema(schemaInput)
	if err != nil {
		return nil, fmt.Errorf("failed to build addon schema: %w", err)
	}

	// 2. Start with instance parameters (using Config field from ComponentAddon)
	parameters, err := extractParameters(input.Instance.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to extract addon instance parameters: %w", err)
	}

	// 3. Merge environment overrides if present
	if input.EnvSettings != nil && input.EnvSettings.Spec.AddonOverrides != nil {
		// AddonOverrides structure: map[addonName]map[instanceId]overrides
		addonName := input.Addon.Name
		instanceID := input.Instance.InstanceID

		if addonOverrides, ok := input.EnvSettings.Spec.AddonOverrides[addonName]; ok {
			if instanceOverride, ok := addonOverrides[instanceID]; ok {
				envOverrides, err := extractParametersFromRawExtension(&instanceOverride)
				if err != nil {
					return nil, fmt.Errorf("failed to extract addon environment overrides: %w", err)
				}
				parameters = deepMerge(parameters, envOverrides)
			}
		}
	}

	// 4. Apply schema defaults
	parameters = schema.ApplyDefaults(parameters, structural)
	ctx["parameters"] = parameters

	// 5. Add addon metadata
	addonMeta := map[string]any{
		"name":       input.Addon.Name,
		"instanceId": input.Instance.InstanceID,
	}
	ctx["addon"] = addonMeta

	// 6. Add component reference
	componentMeta := map[string]any{
		"name": input.Component.Name,
	}
	if input.Component.Namespace != "" {
		componentMeta["namespace"] = input.Component.Namespace
	}
	ctx["component"] = componentMeta

	// 7. Add environment
	if input.Environment != "" {
		ctx["environment"] = input.Environment
	}

	// 8. Add additional metadata
	if len(input.AdditionalMetadata) > 0 {
		ctx["metadata"] = input.AdditionalMetadata
	}

	return ctx, nil
}

// extractParametersFromRawExtension converts a runtime.RawExtension to a map[string]any.
// This is similar to extractParameters but operates on a runtime.RawExtension directly.
func extractParametersFromRawExtension(raw *runtime.RawExtension) (map[string]any, error) {
	if raw == nil || raw.Raw == nil {
		return make(map[string]any), nil
	}

	var params map[string]any
	if err := json.Unmarshal(raw.Raw, &params); err != nil {
		return nil, fmt.Errorf("failed to unmarshal parameters: %w", err)
	}

	return params, nil
}
