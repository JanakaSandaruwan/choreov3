// Copyright 2025 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package context

import (
	apiextschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openchoreo/openchoreo/api/v1alpha1"
)

// ComponentContextInput contains all inputs needed to build a component rendering context.
type ComponentContextInput struct {
	// Component is the component definition.
	Component *v1alpha1.Component

	// ComponentTypeDefinition is the type definition for the component.
	ComponentTypeDefinition *v1alpha1.ComponentTypeDefinition

	// EnvSettings contains environment-specific overrides.
	// Can be nil if no overrides are needed.
	EnvSettings *v1alpha1.EnvSettings

	// Workload contains the workload specification with the built image.
	Workload *v1alpha1.Workload

	// Environment is the name of the environment being rendered for.
	Environment string

	// AdditionalMetadata contains extra contextual information.
	AdditionalMetadata map[string]string
}

// AddonContextInput contains all inputs needed to build an addon rendering context.
type AddonContextInput struct {
	// Addon is the addon definition.
	Addon *v1alpha1.Addon

	// Instance contains the specific instance configuration.
	Instance v1alpha1.ComponentAddon

	// Component is the component this addon is being applied to.
	Component *v1alpha1.Component

	// EnvSettings contains environment-specific addon overrides.
	// Can be nil if no overrides are needed.
	EnvSettings *v1alpha1.EnvSettings

	// Environment is the name of the environment being rendered for.
	Environment string

	// AdditionalMetadata contains extra contextual information.
	AdditionalMetadata map[string]string
}

// SchemaInput contains schema information for applying defaults.
type SchemaInput struct {
	// ParametersSchema is the parameters schema definition.
	ParametersSchema *runtime.RawExtension

	// EnvOverridesSchema is the envOverrides schema definition.
	EnvOverridesSchema *runtime.RawExtension

	// Structural is the compiled structural schema (cached).
	Structural *apiextschema.Structural
}
