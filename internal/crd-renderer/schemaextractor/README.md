# Schema Extractor

This package converts compact schema shorthand syntax into Kubernetes OpenAPI v3 JSON schemas, making it easy to define configuration parameters without writing verbose schema definitions.

## Quick Example

**Input** (shorthand YAML):
```yaml
name: string
replicas: 'integer | default=1'
environment: 'string | enum=dev,staging,prod | default=dev'
description: 'string | default=""'
```

**Output** (OpenAPI v3 JSON Schema):
```json
{
  "type": "object",
  "required": ["name"],
  "properties": {
    "name": {
      "type": "string"
    },
    "replicas": {
      "type": "integer",
      "default": 1
    },
    "environment": {
      "type": "string",
      "default": "dev",
      "enum": ["dev", "staging", "prod"]
    },
    "description": {
      "type": "string",
      "default": ""
    }
  }
}
```

## Usage

```go
import "github.com/wso2/openchoreo/internal/crd-renderer/schemaextractor"

// Define your schema using the shorthand syntax
fields := map[string]any{
    "name":        "string",
    "replicas":    "integer | default=1",
    "environment": "string | enum=dev,staging,prod | default=dev",
    "description": `string | default=""`,
}

// Convert to OpenAPI v3 JSON Schema
converter := schemaextractor.NewConverter(nil)
schema, err := converter.Convert(fields)
if err != nil {
    // handle error
}

// Use the generated schema for validation, CRD generation, etc.
```

## Syntax Overview

- **Primitive types** – `string`, `integer`, `number`, `boolean`, `object`, `[]string`, `map<string>`.
- **Custom types** – reference a named entry declared under `definition.Types` (e.g. `[]MountConfig`).
- **Constraints** – append `|`-separated markers:
  `string | required=true | default=foo | pattern=^[a-z]+$`.
  Only the first `|` (between the type and the constraint section) is required; subsequent markers can be space- or pipe-separated (`string | required=true default=foo` works).
- **Required by default** – fields are considered required unless they declare `default=` or `required=false`.
- **Arrays** – `[]Type`, `array<Type>`, `[]map<string>`, and references to custom types such as `[]MountConfig`. Parenthesized forms like `[](map<string>)` are not supported.
- **Maps** – `map<Type>` or `map[Key]Type` (keys must resolve to strings for OpenAPI compatibility).
- **Object literals** – nested field maps represented as normal YAML/JSON objects.

## Supported Constraint Markers

| Marker | Description |
| ------ | ----------- |
| `required=true|false` | Force field to be (non-)required. Fields without this marker are required by default unless they have an explicit `default`. |
| `default=<value>` | Supplies a default. Primitive values can be quoted (`default=""`, `default='v1'`) and are unquoted by the parser so they render as expected. |
| `enum=a,b,c` | Enumerated values (parsed according to the field type). |
| `pattern`, `minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum` | Standard JSON schema validations. |
| `minItems`, `maxItems`, `uniqueItems` | Array-related constraints. |
| `minLength`, `maxLength` | String length validations. |
| `minProperties`, `maxProperties` | Object/map property counts. |
| `multipleOf` | Numeric multiplier constraint. |
| `nullable=true` | Allows `null` values. |
| `title`, `description`, `format`, `example` | Informational metadata. |

Unknown markers are ignored, making it safe to introduce custom annotations upstream.

## Beyond Plain JSON Schema

`schemaextractor` does more than emit JSON schema documents:

1. **Structural schema generation** – via `schema.ToStructural`, converting the JSON schema into the Kubernetes `Structural` form that powers defaulting.
2. **Default extraction and application** – callers use `schema.ApplyDefaults` to run the Kubernetes defaulting algorithm against arbitrary maps. This is how the renderer populates defaults after merging component/environment inputs.
3. **Literal handling** – the parser keeps constraint tokens as raw strings; `parseValueForType` includes special handling for quoted primitives so that shorthands like `default=""` become actual empty strings when defaulting is applied.

These utilities let the rendering pipeline consume the shorthand once, then reuse it for JSON schema emission, structural-schema validation, and defaulting without reimplementing conversion logic elsewhere.
