// Copyright 2025 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package patch

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/yaml"
)

func TestApplyPatch(t *testing.T) {
	t.Parallel()

	render := func(v any, _ map[string]any) (any, error) {
		return v, nil
	}

	tests := []struct {
		name       string
		initial    string
		operations []JSONPatchOperation
		want       string
	}{
		{
			name: "add env entry via array filter",
			initial: `
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: app
          image: app:v1
          env:
            - name: A
              value: "1"
`,
			operations: []JSONPatchOperation{
				{
					Op:   "add",
					Path: "/spec/template/spec/containers/[?(@.name=='app')]/env/-",
					Value: map[string]any{
						"name":  "B",
						"value": "2",
					},
				},
			},
			want: `
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: app
          image: app:v1
          env:
            - name: A
              value: "1"
            - name: B
              value: "2"
`,
		},
		{
			name: "replace image using index path",
			initial: `
spec:
  template:
    spec:
      containers:
        - name: app
          image: app:v1
`,
			operations: []JSONPatchOperation{
				{
					Op:    "replace",
					Path:  "/spec/template/spec/containers/0/image",
					Value: "app:v2",
				},
			},
			want: `
spec:
  template:
    spec:
      containers:
        - name: app
          image: app:v2
`,
		},
		{
			name: "remove first env entry",
			initial: `
spec:
  template:
    spec:
      containers:
        - name: app
          env:
            - name: A
              value: "1"
            - name: B
              value: "2"
`,
			operations: []JSONPatchOperation{
				{
					Op:   "remove",
					Path: "/spec/template/spec/containers/[?(@.name=='app')]/env/0",
				},
			},
			want: `
spec:
  template:
    spec:
      containers:
        - name: app
          env:
            - name: B
              value: "2"
`,
		},
		{
			name: "mergeShallow annotations without clobbering existing",
			initial: `
spec:
  template:
    metadata:
      annotations:
        existing: "true"
`,
			operations: []JSONPatchOperation{
				{
					Op:   "mergeShallow",
					Path: "/spec/template/metadata/annotations",
					Value: map[string]any{
						"platform": "enabled",
					},
				},
			},
			want: `
spec:
  template:
    metadata:
      annotations:
        existing: "true"
        platform: enabled
`,
		},
		{
			name: "mergeShallow replaces nested maps instead of deep merging",
			initial: `
spec:
  template:
    metadata:
      annotations:
        nested:
          keep: retained
        sibling: present
`,
			operations: []JSONPatchOperation{
				{
					Op:   "mergeShallow",
					Path: "/spec/template/metadata/annotations",
					Value: map[string]any{
						"nested": map[string]any{
							"added": "new",
						},
					},
				},
			},
			want: `
spec:
  template:
    metadata:
      annotations:
        nested:
          added: new
        sibling: present
`,
		},
		{
			name: "test operation success",
			initial: `
spec:
  template:
    metadata:
      annotations:
        existing: "true"
`,
			operations: []JSONPatchOperation{
				{
					Op:    "test",
					Path:  "/spec/template/metadata/annotations/existing",
					Value: "true",
				},
				{
					Op:    "replace",
					Path:  "/spec/template/metadata/annotations/existing",
					Value: "updated",
				},
			},
			want: `
spec:
  template:
    metadata:
      annotations:
        existing: updated
`,
		},
		{
			name: "add env entry for multiple matches",
			initial: `
spec:
  template:
    spec:
      containers:
        - name: app
          role: worker
          env: []
        - name: logger
          role: worker
          env: []
`,
			operations: []JSONPatchOperation{
				{
					Op:   "add",
					Path: "/spec/template/spec/containers/[?(@.role=='worker')]/env/-",
					Value: map[string]any{
						"name":  "SHARED",
						"value": "true",
					},
				},
			},
			want: `
spec:
  template:
    spec:
      containers:
        - name: app
          role: worker
          env:
            - name: SHARED
              value: "true"
        - name: logger
          role: worker
          env:
            - name: SHARED
              value: "true"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var resource map[string]any
			if err := yaml.Unmarshal([]byte(tt.initial), &resource); err != nil {
				t.Fatalf("failed to unmarshal initial YAML: %v", err)
			}

			for _, op := range tt.operations {
				if err := ApplyOperation(resource, op, nil, render); err != nil {
					t.Fatalf("ApplyOperation error = %v", err)
				}
			}

			var wantObj map[string]any
			if err := yaml.Unmarshal([]byte(tt.want), &wantObj); err != nil {
				t.Fatalf("failed to unmarshal expected YAML: %v", err)
			}

			if diff := cmpDiff(wantObj, resource); diff != "" {
				t.Fatalf("resource mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestApplyPatchTestOpFailure(t *testing.T) {
	render := func(v any, _ map[string]any) (any, error) {
		return v, nil
	}

	initial := `
spec:
  template:
    metadata:
      annotations:
        existing: "true"
`

	var resource map[string]any
	if err := yaml.Unmarshal([]byte(initial), &resource); err != nil {
		t.Fatalf("failed to unmarshal initial YAML: %v", err)
	}

	op := JSONPatchOperation{
		Op:    "test",
		Path:  "/spec/template/metadata/annotations/existing",
		Value: "false",
	}

	if err := ApplyOperation(resource, op, nil, render); err == nil {
		t.Fatalf("expected test operation to fail but succeeded")
	}
}

func TestApplySpec_ForEachAndWhere(t *testing.T) {
	t.Parallel()

	resources := []map[string]any{
		{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name": "api",
				"annotations": map[string]any{
					"owner": "platform",
				},
			},
		},
		{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name": "worker",
				"annotations": map[string]any{
					"owner": "ops",
				},
			},
		},
	}

	spec := PatchSpec{
		ForEach: "addons",
		Var:     "addon",
		Target: TargetSpec{
			Kind:  "Deployment",
			Where: "match-addon-name",
		},
		Operations: []JSONPatchOperation{
			{
				Op:    "mergeShallow",
				Path:  "/metadata/annotations",
				Value: "annotationValue",
			},
		},
	}

	render := func(expr any, inputs map[string]any) (any, error) {
		switch v := expr.(type) {
		case string:
			switch v {
			case "addons":
				return []any{
					map[string]any{"name": "api", "key": "team", "value": "platform"},
					map[string]any{"name": "worker", "key": "team", "value": "ops"},
				}, nil
			case "annotationValue":
				addon := inputs["addon"].(map[string]any)
				key := addon["key"].(string)
				return map[string]any{key: addon["value"]}, nil
			case "match-addon-name":
				addon := inputs["addon"].(map[string]any)
				resource := inputs["resource"].(map[string]any)
				metadata, _ := resource["metadata"].(map[string]any)
				name, _ := metadata["name"].(string)
				return name == addon["name"], nil
			default:
				return v, nil
			}
		default:
			return expr, nil
		}
	}

	err := ApplySpec(resources, spec, map[string]any{}, func(map[string]any, string) bool { return true }, render, nil)
	if err != nil {
		t.Fatalf("ApplySpec error = %v", err)
	}

	expected := []map[string]any{
		{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name": "api",
				"annotations": map[string]any{
					"owner": "platform",
					"team":  "platform",
				},
			},
		},
		{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name": "worker",
				"annotations": map[string]any{
					"owner": "ops",
					"team":  "ops",
				},
			},
		},
	}

	if diff := cmp.Diff(expected, resources); diff != "" {
		t.Fatalf("resources mismatch (-want +got):\n%s", diff)
	}
}

func TestApplySpec_SkipsMissingData(t *testing.T) {
	t.Parallel()

	errMissing := errors.New("missing data")

	resources := []map[string]any{
		{
			"metadata": map[string]any{
				"name": "api",
				"annotations": map[string]any{
					"owner": "platform",
				},
			},
		},
		{
			"metadata": map[string]any{
				"name": "worker",
				"annotations": map[string]any{
					"owner": "ops",
				},
			},
		},
	}

	spec := PatchSpec{
		ForEach: "addons",
		Var:     "addon",
		Target: TargetSpec{
			Where: "match-addon-name",
		},
		Operations: []JSONPatchOperation{
			{
				Op:    "mergeShallow",
				Path:  "/metadata/annotations",
				Value: "annotationValue",
			},
		},
	}

	render := func(expr any, inputs map[string]any) (any, error) {
		switch v := expr.(type) {
		case string:
			switch v {
			case "addons":
				return []any{
					map[string]any{"name": "api", "key": "team", "value": "platform"},
					map[string]any{"name": "worker", "key": "team", "value": "ops"},
				}, nil
			case "annotationValue":
				addon := inputs["addon"].(map[string]any)
				key := addon["key"].(string)
				return map[string]any{key: addon["value"]}, nil
			case "match-addon-name":
				addon := inputs["addon"].(map[string]any)
				resource := inputs["resource"].(map[string]any)
				metadata, _ := resource["metadata"].(map[string]any)
				name, _ := metadata["name"].(string)
				if name == "worker" {
					return nil, errMissing
				}
				return name == addon["name"], nil
			default:
				return v, nil
			}
		default:
			return expr, nil
		}
	}

	err := ApplySpec(
		resources,
		spec,
		map[string]any{},
		func(map[string]any, string) bool { return true },
		render,
		func(err error) bool { return errors.Is(err, errMissing) },
	)
	if err != nil {
		t.Fatalf("ApplySpec error = %v", err)
	}

	expected := []map[string]any{
		{
			"metadata": map[string]any{
				"name": "api",
				"annotations": map[string]any{
					"owner": "platform",
					"team":  "platform",
				},
			},
		},
		{
			"metadata": map[string]any{
				"name": "worker",
				"annotations": map[string]any{
					"owner": "ops",
				},
			},
		},
	}

	if diff := cmp.Diff(expected, resources); diff != "" {
		t.Fatalf("resources mismatch (-want +got):\n%s", diff)
	}
}

func cmpDiff(expected, actual map[string]any) string {
	wantJSON, _ := json.Marshal(expected)
	gotJSON, _ := json.Marshal(actual)

	var wantNorm, gotNorm any
	_ = json.Unmarshal(wantJSON, &wantNorm)
	_ = json.Unmarshal(gotJSON, &gotNorm)

	if diff := cmp.Diff(wantNorm, gotNorm); diff != "" {
		return diff
	}
	return ""
}
