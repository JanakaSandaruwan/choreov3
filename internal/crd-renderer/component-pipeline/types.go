// Copyright 2025 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package componentpipeline

import (
	"github.com/openchoreo/openchoreo/api/v1alpha1"
	"github.com/openchoreo/openchoreo/internal/crd-renderer/template"
)

// Pipeline orchestrates the complete rendering workflow for Component resources.
// It combines ComponentEnvSnapshot and EnvSettings to generate fully resolved
// Kubernetes resource manifests.
type Pipeline struct {
	templateEngine *template.Engine
	options        RenderOptions
}

// RenderInput contains all inputs needed to render a component's resources.
type RenderInput struct {
	// Snapshot contains the immutable snapshot of the component and its dependencies.
	// Required.
	Snapshot *v1alpha1.ComponentEnvSnapshot

	// Settings contains environment-specific overrides for the component.
	// Optional - if nil, no environment overrides are applied.
	Settings *v1alpha1.EnvSettings

	// Metadata contains additional metadata to include in rendered resources.
	// This can include labels, annotations, or other contextual information.
	// Optional.
	Metadata map[string]string
}

// RenderOutput contains the results of the rendering process.
type RenderOutput struct {
	// Resources is the list of fully rendered Kubernetes resource manifests.
	Resources []map[string]any

	// Metadata contains information about the rendering process.
	Metadata *RenderMetadata
}

// RenderMetadata contains information about the rendering process.
type RenderMetadata struct {
	// ResourceCount is the total number of resources rendered.
	ResourceCount int

	// BaseResourceCount is the number of resources from the ComponentTypeDefinition.
	BaseResourceCount int

	// AddonCount is the number of addons processed.
	AddonCount int

	// AddonResourceCount is the number of resources created by addons.
	AddonResourceCount int

	// Warnings contains non-fatal issues encountered during rendering.
	Warnings []string
}

// RenderOptions configures the rendering behavior.
type RenderOptions struct {
	// EnableValidation enables resource validation after rendering.
	EnableValidation bool

	// StrictMode causes the pipeline to fail on warnings.
	StrictMode bool

	// ResourceLabels are additional labels to add to all rendered resources.
	ResourceLabels map[string]string

	// ResourceAnnotations are additional annotations to add to all rendered resources.
	ResourceAnnotations map[string]string
}

// DefaultRenderOptions returns the default rendering options.
func DefaultRenderOptions() RenderOptions {
	return RenderOptions{
		EnableValidation:    true,
		StrictMode:          false,
		ResourceLabels:      map[string]string{},
		ResourceAnnotations: map[string]string{},
	}
}
