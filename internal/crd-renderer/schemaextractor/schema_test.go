// Copyright 2025 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package schemaextractor

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConverter_JSONMatchesExpected(t *testing.T) {
	const typesYAML = ``
	const schemaYAML = `
name: string
replicas: 'integer | default=1'
`
	const expected = `{
  "type": "object",
  "required": [
    "name"
  ],
  "properties": {
    "name": {
      "type": "string"
    },
    "replicas": {
      "type": "integer",
      "default": 1
    }
  }
}`

	assertConvertedSchema(t, typesYAML, schemaYAML, expected)
}

func TestConverter_ArrayDefaultParsing(t *testing.T) {
	const typesYAML = `
Item:
  name: 'string | default=default-name'
`
	const schemaYAML = `
items: '[]Item | default=[{"name":"custom"}]'
`
	const expected = `{
  "type": "object",
  "properties": {
    "items": {
      "type": "array",
      "default": [
        {
          "name": "custom"
        }
      ],
      "items": {
        "type": "object",
        "properties": {
          "name": {
            "type": "string",
            "default": "default-name"
          }
        }
      }
    }
  }
}`

	assertConvertedSchema(t, typesYAML, schemaYAML, expected)
}

func TestConverter_DefaultRequiredBehaviour(t *testing.T) {
	const typesYAML = ``
	const schemaYAML = `
mustProvide: string
hasDefault: 'integer | default=5'
explicitOpt: 'boolean | required=false'
`
	const expected = `{
  "type": "object",
  "required": [
    "mustProvide"
  ],
  "properties": {
    "explicitOpt": {
      "type": "boolean"
    },
    "hasDefault": {
      "type": "integer",
      "default": 5
    },
    "mustProvide": {
      "type": "string"
    }
  }
}`

	assertConvertedSchema(t, typesYAML, schemaYAML, expected)
}

func TestConverter_CustomTypeJSONMatchesExpected(t *testing.T) {
	const typesYAML = `
Resources:
  cpu: 'string | default=100m'
  memory: string
`
	const schemaYAML = `
resources: Resources
`
	const expected = `{
  "type": "object",
  "required": [
    "resources"
  ],
  "properties": {
    "resources": {
      "type": "object",
      "required": [
        "memory"
      ],
      "properties": {
        "cpu": {
          "type": "string",
          "default": "100m"
        },
        "memory": {
          "type": "string"
        }
      }
    }
  }
}`

	assertConvertedSchema(t, typesYAML, schemaYAML, expected)
}

func TestConverter_ArraySyntaxVariants(t *testing.T) {
	const typesYAML = `
Item:
  name: string
`
	const listSchema = `
items: '[]Item'
`
	const arraySchema = `
items: 'array<Item>'
`

	converter := NewConverter(parseYAMLMap(t, typesYAML))

	list, err := converter.Convert(parseYAMLMap(t, listSchema))
	if err != nil {
		t.Fatalf("Convert for []Item returned error: %v", err)
	}
	array, err := converter.Convert(parseYAMLMap(t, arraySchema))
	if err != nil {
		t.Fatalf("Convert for array<Item> returned error: %v", err)
	}

	listJSON, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("failed to marshal list schema: %v", err)
	}
	arrayJSON, err := json.Marshal(array)
	if err != nil {
		t.Fatalf("failed to marshal array schema: %v", err)
	}
	if string(listJSON) != string(arrayJSON) {
		t.Fatalf("expected []Item and array<Item> to produce identical schemas\nlist: %s\narray: %s", string(listJSON), string(arrayJSON))
	}
}

func TestConverter_ArrayOfMaps(t *testing.T) {
	const schemaYAML = `
tags: '[]map<string> | default=[]'
`
	const expected = `{
  "type": "object",
  "properties": {
    "tags": {
      "type": "array",
      "default": [],
      "items": {
        "type": "object",
        "additionalProperties": {
          "type": "string"
        }
      }
    }
  }
}`

	assertConvertedSchema(t, "", schemaYAML, expected)
}

func TestConverter_ParenthesizedArraySyntaxRejected(t *testing.T) {
	const schemaYAML = `
tags: "[](map<string>)"
`

	converter := NewConverter(nil)
	_, err := converter.Convert(parseYAMLMap(t, schemaYAML))
	if err == nil {
		t.Fatalf("expected error for unsupported syntax [](map<string>)")
	}
}

func TestConverter_CombinedConstraintsSpacing(t *testing.T) {
	const schemaYAML = `
field: string | required=false default=foo pattern=^[a-z]+$
`
	const expected = `{
  "type": "object",
  "properties": {
    "field": {
      "type": "string",
      "default": "foo",
      "pattern": "^[a-z]+$"
    }
  }
}`

	assertConvertedSchema(t, "", schemaYAML, expected)
}

func TestConverter_EnumParsing(t *testing.T) {
	const schemaYAML = `
level: string | enum=debug,info,warn | default=info
`
	const expected = `{
  "type": "object",
  "properties": {
    "level": {
      "type": "string",
      "default": "info",
      "enum": [
        "debug",
        "info",
        "warn"
      ]
    }
  }
}`

	assertConvertedSchema(t, "", schemaYAML, expected)
}

func assertSchemaJSON(t *testing.T, schema any, expected string) {
	t.Helper()

	actualBytes, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal schema: %v", err)
	}

	if string(actualBytes) != expected {
		t.Fatalf("schema JSON mismatch\nexpected:\n%s\nactual:\n%s", expected, string(actualBytes))
	}
}

func assertConvertedSchema(t *testing.T, typesYAML, schemaYAML, expected string) {
	t.Helper()

	var types map[string]any
	if strings.TrimSpace(typesYAML) != "" {
		types = parseYAMLMap(t, typesYAML)
	}
	root := parseYAMLMap(t, schemaYAML)

	converter := NewConverter(types)
	schema, err := converter.Convert(root)
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	assertSchemaJSON(t, schema, expected)
}

func parseYAMLMap(t *testing.T, doc string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := yaml.Unmarshal([]byte(doc), &out); err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}
	return out
}
