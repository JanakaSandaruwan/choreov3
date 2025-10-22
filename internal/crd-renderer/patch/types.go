// Copyright 2025 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package patch

// JSONPatchOperation represents a single patch operation in a JSON Patch specification.
type JSONPatchOperation struct {
	Op    string `yaml:"op"`
	Path  string `yaml:"path"`
	Value any    `yaml:"value,omitempty"`
}

// TargetSpec describes how to locate a resource when applying patches.
type TargetSpec struct {
	Kind    string `yaml:"kind,omitempty"`
	Group   string `yaml:"group,omitempty"`
	Version string `yaml:"version,omitempty"`
	Name    string `yaml:"name,omitempty"`
	Where   string `yaml:"where,omitempty"`
}

// PatchSpec represents a set of operations to apply to matching resources.
type PatchSpec struct {
	ForEach    string               `yaml:"forEach,omitempty"`
	Var        string               `yaml:"var,omitempty"`
	Target     TargetSpec           `yaml:"target"`
	Operations []JSONPatchOperation `yaml:"operations"`
}
