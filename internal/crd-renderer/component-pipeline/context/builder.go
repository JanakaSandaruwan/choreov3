// Copyright 2025 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

// Package context provides context building functionality for component rendering.
//
// The context builders create CEL evaluation contexts by merging parameters,
// applying environment overrides, and applying schema defaults.
package context

import (
	"fmt"

	"github.com/openchoreo/openchoreo/api/v1alpha1"
)

// BuildFromSnapshot builds a component context directly from a ComponentEnvSnapshot.
// This is a convenience function that extracts the necessary data from the snapshot.
func BuildFromSnapshot(
	snapshot *v1alpha1.ComponentEnvSnapshot,
	settings *v1alpha1.EnvSettings,
	additionalMetadata map[string]string,
) (map[string]any, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("snapshot is nil")
	}

	input := &ComponentContextInput{
		Component:               &snapshot.Spec.Component,
		ComponentTypeDefinition: &snapshot.Spec.ComponentTypeDefinition,
		Workload:                &snapshot.Spec.Workload,
		Environment:             snapshot.Spec.Environment,
		EnvSettings:             settings,
		AdditionalMetadata:      additionalMetadata,
	}

	return BuildComponentContext(input)
}

// BuildAddonFromSnapshot builds an addon context for a specific addon instance from a snapshot.
// This is a convenience function for processing addons in a ComponentEnvSnapshot.
func BuildAddonFromSnapshot(
	snapshot *v1alpha1.ComponentEnvSnapshot,
	addon *v1alpha1.Addon,
	instance v1alpha1.ComponentAddon,
	settings *v1alpha1.EnvSettings,
	additionalMetadata map[string]string,
) (map[string]any, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("snapshot is nil")
	}
	if addon == nil {
		return nil, fmt.Errorf("addon is nil")
	}

	input := &AddonContextInput{
		Addon:              addon,
		Instance:           instance,
		Component:          &snapshot.Spec.Component,
		Environment:        snapshot.Spec.Environment,
		EnvSettings:        settings,
		AdditionalMetadata: additionalMetadata,
	}

	return BuildAddonContext(input)
}
