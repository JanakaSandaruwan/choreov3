// Copyright 2025 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package template

import (
	"encoding/json"
	"fmt"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestEngineRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		inputs   string
		want     string
	}{
		{
			name: "string literal without expressions",
			template: `
plain: hello
`,
			inputs: `{}`,
			want: `plain: hello
`,
		},
		{
			name: "string interpolation and numeric result",
			template: `
message: "${metadata.name} has ${spec.replicas} replicas"
numeric: ${spec.replicas}
`,
			inputs: `{
  "metadata": {"name": "checkout"},
  "spec": {"replicas": 2}
}`,
			want: `message: checkout has 2 replicas
numeric: 2
`,
		},
		{
			name: "map with omit and merge helpers",
			template: `
annotations:
  base: '${merge({"team": "platform"}, metadata.labels)}'
  optional: '${has(spec.flag) && spec.flag ? {"enabled": "true"} : omit()}'
`,
			inputs: `{
  "metadata": {"labels": {"team": "payments", "region": "us"}},
  "spec": {"flag": true}
}`,
			want: `annotations:
  base:
    region: us
    team: payments
  optional:
    enabled: "true"
`,
		},
		{
			name: "array forEach via CEL comprehension",
			template: `
env: '${containers.map(c, {"name": c.name, "image": c.image})}'
`,
			inputs: `{
  "containers": [
    {"name": "app", "image": "app:1.0"},
    {"name": "sidecar", "image": "sidecar:latest"}
  ]
}`,
			want: `env:
- image: app:1.0
  name: app
- image: sidecar:latest
  name: sidecar
`,
		},
		{
			name: "full object literal",
			template: `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${metadata.name}
spec:
  replicas: ${spec.replicas}
  template:
    metadata:
      labels: ${metadata.labels}
`,
			inputs: `{
  "metadata": {"name": "web", "labels": {"app": "web"}},
  "spec": {"replicas": 3}
}`,
			want: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 3
  template:
    metadata:
      labels:
        app: web
`,
		},
		{
			name: "sanitizeK8sResourceName with single argument",
			template: `
name: ${sanitizeK8sResourceName("Hello World!")}
`,
			inputs: `{}`,
			want: `name: helloworld
`,
		},
		{
			name: "sanitizeK8sResourceName with multiple arguments",
			template: `
name: ${sanitizeK8sResourceName("my-app", "-", "v1.2.3")}
`,
			inputs: `{}`,
			want: `name: myappv123
`,
		},
		{
			name: "sanitizeK8sResourceName with many arguments",
			template: `
name: ${sanitizeK8sResourceName("front", "-", "end", "-", "prod", "-", "us-west", "-", "99")}
`,
			inputs: `{}`,
			want: `name: frontendproduswest99
`,
		},
		{
			name: "sanitizeK8sResourceName with dynamic values",
			template: `
name: ${sanitizeK8sResourceName(metadata.name, "-", spec.version)}
`,
			inputs: `{
  "metadata": {"name": "payment-service"},
  "spec": {"version": "v2.0"}
}`,
			want: `name: paymentservicev20
`,
		},
	}

	engine := NewEngine()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var tpl any
			if err := yaml.Unmarshal([]byte(tt.template), &tpl); err != nil {
				t.Fatalf("failed to unmarshal template: %v", err)
			}

			var input map[string]any
			if err := json.Unmarshal([]byte(tt.inputs), &input); err != nil {
				t.Fatalf("failed to unmarshal inputs: %v", err)
			}

			rendered, err := engine.Render(tpl, input)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}

			cleaned := RemoveOmittedFields(rendered)

			got, err := yaml.Marshal(cleaned)
			if err != nil {
				t.Fatalf("failed to marshal result: %v", err)
			}

			if err := compareYAML(tt.want, string(got)); err != nil {
				t.Fatalf("rendered output mismatch: %v", err)
			}
		})
	}
}

func compareYAML(expected, actual string) error {
	var wantObj, gotObj any
	if err := yaml.Unmarshal([]byte(expected), &wantObj); err != nil {
		return fmt.Errorf("failed to unmarshal expected YAML: %w", err)
	}
	if err := yaml.Unmarshal([]byte(actual), &gotObj); err != nil {
		return fmt.Errorf("failed to unmarshal actual YAML: %w", err)
	}

	wantBytes, _ := yaml.Marshal(wantObj)
	gotBytes, _ := yaml.Marshal(gotObj)

	if string(wantBytes) != string(gotBytes) {
		return fmt.Errorf("want:\n%s\n\ngot:\n%s\n", wantBytes, gotBytes)
	}
	return nil
}

func TestIsMissingDataError(t *testing.T) {
	engine := NewEngine()

	tests := []struct {
		name          string
		expression    string
		inputs        map[string]any
		wantIsMissing bool
	}{
		{
			name:          "missing map key - runtime error",
			expression:    "${data.missingKey}",
			inputs:        map[string]any{"data": map[string]any{"existingKey": "value"}},
			wantIsMissing: true,
		},
		{
			name:          "undeclared variable - compile error",
			expression:    "${undeclaredVariable}",
			inputs:        map[string]any{},
			wantIsMissing: true,
		},
		{
			name:          "valid expression - no error",
			expression:    "${data.key}",
			inputs:        map[string]any{"data": map[string]any{"key": "value"}},
			wantIsMissing: false,
		},
		{
			name:          "type error - not missing data",
			expression:    "${1 + 'string'}",
			inputs:        map[string]any{},
			wantIsMissing: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := engine.Render(tt.expression, tt.inputs)

			if tt.wantIsMissing {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if !IsMissingDataError(err) {
					t.Errorf("IsMissingDataError() = false, want true for error: %v", err)
				}
			} else {
				if err != nil && IsMissingDataError(err) {
					t.Errorf("IsMissingDataError() = true, want false for error: %v", err)
				}
			}
		})
	}
}
