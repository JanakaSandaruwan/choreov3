// Copyright 2025 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package template

import (
	"fmt"
	"hash/fnv"
	"maps"
	"reflect"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/ext"
	"github.com/google/cel-go/parser"

	"github.com/openchoreo/openchoreo/internal/dataplane/kubernetes"
)

// BaseCELExtensions returns the CEL extensions used across OpenChoreo.
// This includes optional types, common utility extensions for strings, encoding,
// math, lists, sets, two-variable comprehensions, and OpenChoreo custom functions.
func BaseCELExtensions() []cel.EnvOption {
	opts := []cel.EnvOption{
		cel.OptionalTypes(),
		ext.Strings(),
		ext.Encoders(),
		ext.Math(),
		ext.Lists(),
		ext.Sets(),
		ext.TwoVarComprehensions(),
	}
	return append(opts, CustomFunctions()...)
}

// omitValue is a sentinel used to mark values that should be pruned after rendering.
// The template engine recognizes this sentinel and removes the containing field from output.
type omitValue struct{}

var omitSentinel = &omitValue{}

const omitErrMsg = "__OC_RENDERER_OMIT__"

// omitCELValue is a CEL value type that represents an omitted value.
//
// This internal type allows oc_omit() to return a valid CEL value (rather than an error)
// that can be safely used inside map literals and arrays. The template engine's post-processing
// phase detects the omitSentinel and removes the containing field, map key, or array element
// from the final rendered output.
//
// Implementation notes:
//   - ConvertToNative returns omitSentinel which the pruning logic recognizes
//   - Type() returns a custom "omit" type to distinguish from other CEL values
//   - Equal() only returns true when comparing two omitCELValue instances
//
// See CustomFunctions() documentation for usage examples.
type omitCELValue struct{}

var (
	omitCEL     = &omitCELValue{}
	omitTypeVal = cel.ObjectType("omit")
)

// CEL ref.Val interface implementation for omitCELValue
func (o *omitCELValue) ConvertToNative(typeDesc reflect.Type) (interface{}, error) {
	return omitSentinel, nil
}

func (o *omitCELValue) ConvertToType(typeVal ref.Type) ref.Val {
	return o
}

func (o *omitCELValue) Equal(other ref.Val) ref.Val {
	if _, ok := other.(*omitCELValue); ok {
		return types.True
	}
	return types.False
}

func (o *omitCELValue) Type() ref.Type {
	return omitTypeVal
}

func (o *omitCELValue) Value() interface{} {
	return omitSentinel
}

// CustomFunctions returns the CEL environment options for custom template functions.
//
// These functions provide additional capabilities beyond the standard CEL-go extensions,
// designed for use in CEL-based templates throughout OpenChoreo. All custom functions use
// the "oc_" prefix to avoid potential conflicts with upstream CEL-go.
//
// # Available Functions
//
// oc_omit() - Remove fields, map keys, or array items from rendered output
//
// oc_merge(map1, map2, ...mapN) - Shallow merge of multiple maps
//
// oc_generate_name(...strings) - Generate valid Kubernetes resource names
//
// oc_hash(string) - Generate 8-character hash from input string
//
// envFrom(metadataName) - Generate envFrom array for container configurations
//
// volumeMounts() - Generate volumeMounts array for container file configurations
//
// volumes(metadataName) - Generate volumes array for container file configurations
//
// # oc_omit() - Conditional Omission
//
// Returns a sentinel value that is removed during post-processing. Supports two use cases:
//
// Use Case 1: Remove entire fields from YAML/JSON structure
//
//	metadata:
//	  annotations: ${has(spec.annotations) ? spec.annotations : oc_omit()}
//	  labels:
//	    version: ${has(spec.version) ? spec.version : oc_omit()}
//
// Result when spec.annotations and spec.version are undefined:
//
//	metadata:
//	  labels: {}
//
// Use Case 2: Remove map keys or array items within CEL expressions
//
//	# Conditional map keys
//	labels: ${{"app": metadata.name, "env": has(spec.env) ? spec.env : oc_omit()}}
//
//	# Conditional array items
//	args: ${["--port=8080", spec.debug ? "--debug" : oc_omit(), "--log=info"]}
//
// # oc_merge() - Shallow Map Merge
//
// Merges multiple maps left-to-right, with later maps overriding earlier ones.
// IMPORTANT: This is a shallow merge - nested maps are replaced, not merged recursively.
//
//	# Basic merge
//	env: ${oc_merge(defaults, spec.env, envOverrides)}
//
//	# Inline map literals
//	resources: ${oc_merge({cpu: "100m", memory: "128Mi"}, spec.resources)}
//
//	# Variadic merge (3+ maps)
//	config: ${oc_merge(base, layer1, layer2, layer3)}
//
// Shallow merge behavior:
//
//	base = {resources: {cpu: "100m", memory: "128Mi"}, replicas: 1}
//	override = {resources: {cpu: "200m"}}
//	result = {resources: {cpu: "200m"}, replicas: 1}
//	# Note: memory is LOST because resources map was replaced entirely
//
// # oc_generate_name() - Kubernetes Name Generation
//
// Generates valid Kubernetes DNS subdomain names from arbitrary strings.
// Names are sanitized, truncated to 253 characters, and include an 8-character
// hash suffix for uniqueness.
//
//	# Variadic arguments
//	name: ${oc_generate_name(component.name, environment, "cache")}
//	# "payment-service", "prod", "cache" -> "payment-service-prod-cache-a1b2c3d4"
//
//	# Array input
//	name: ${oc_generate_name([metadata.namespace, metadata.name, "worker"])}
//
//	# Single string (sanitized)
//	name: ${oc_generate_name("My App!")}
//	# "My App!" -> "my-app-e5f6g7h8"
//
// Hash suffix ensures uniqueness even when inputs sanitize to the same string:
//
//	oc_generate_name("my-app")   -> "my-app-abc12345"
//	oc_generate_name("My App!")  -> "my-app-def67890"  # Different hash
//
// # oc_hash() - String Hashing
//
// Generates an 8-character hexadecimal hash from an input string using the FNV-32a
// algorithm. Useful for creating stable, deterministic identifiers or suffixes.
//
// The hash is deterministic - the same input always produces the same output:
//
//	oc_hash("test")  -> "4fdcca5d"  # Always produces this hash
//	oc_hash("test")  -> "4fdcca5d"  # Same input, same output
//
// # envFrom() - Generate EnvFrom Configuration
//
// Generates envFrom entries for Kubernetes container specifications based on configurations.
// This function simplifies the creation of configMapRef and secretRef entries by automatically
// checking the configurations structure and generating appropriate references.
//
// Usage: ${configurations["containerName"].envFrom(metadataName)}
//
// Parameters:
//   - metadataName: The metadata name used for generating ConfigMap and Secret names
//
// Returns: An array of envFrom entries containing configMapRef and/or secretRef as needed
//
// Example usage:
//
//	# Simple usage in Deployment container spec
//	envFrom: ${configurations[parameters.containerName].envFrom(metadata.name)}
//	
//	# Or with literal container name
//	envFrom: ${configurations["main"].envFrom(metadata.name)}
//
//	# Replaces complex manual logic:
//	envFrom: |
//	  ${(has(configurations[parameters.containerName].configs.envs) && configurations[parameters.containerName].configs.envs.size() > 0 ?
//	    [{"configMapRef": {"name": oc_generate_name(metadata.name, "env-configs")}}] : []) +
//	   (has(configurations[parameters.containerName].secrets.envs) && configurations[parameters.containerName].secrets.envs.size() > 0 ?
//	    [{"secretRef": {"name": oc_generate_name(metadata.name, "env-secrets")}}] : [])}
//
// Behavior:
//   - Returns configMapRef entry if container.configs.envs has items
//   - Returns secretRef entry if container.secrets.envs has items
//   - Returns empty array if no environment configurations exist
//   - Returns empty array if container configuration is invalid
//
// Generated names follow the same pattern as oc_generate_name():
//   - ConfigMap: {metadataName}-env-configs-{8-char-hash}
//   - Secret: {metadataName}-env-secrets-{8-char-hash}
//
//
// # volumeMounts() - Generate Volume Mounts Configuration
//
// Generates volumeMounts entries for Kubernetes container specifications based on file configurations.
// This function simplifies the creation of volumeMounts by automatically processing both config files
// and secret files from the container's configurations.
//
// Usage: ${configurations["containerName"].volumeMounts()}
//
// Returns: An array of volumeMounts entries for all files in configs.files and secrets.files
//
// Example usage:
//
//	# Simple usage in Deployment container spec
//	volumeMounts: ${configurations[parameters.containerName].volumeMounts()}
//	
//	# Or with literal container name
//	volumeMounts: ${configurations["main"].volumeMounts()}
//
//	# Replaces complex manual logic:
//	volumeMounts: |
//	  ${has(configurations[parameters.containerName].configs.files) && configurations[parameters.containerName].configs.files.size() > 0 || has(configurations[parameters.containerName].secrets.files) && configurations[parameters.containerName].secrets.files.size() > 0 ?
//	    (has(configurations[parameters.containerName].configs.files) && configurations[parameters.containerName].configs.files.size() > 0 ?
//	      configurations[parameters.containerName].configs.files.map(f, {
//	        "name": "file-mount-"+oc_hash(f.mountPath+"/"+f.name),
//	        "mountPath": f.mountPath+"/"+f.name,
//	        "subPath": f.name
//	      }) : []) +
//	     (has(configurations[parameters.containerName].secrets.files) && configurations[parameters.containerName].secrets.files.size() > 0 ?
//	      configurations[parameters.containerName].secrets.files.map(f, {
//	        "name": "file-mount-"+oc_hash(f.mountPath+"/"+f.name),
//	        "mountPath": f.mountPath+"/"+f.name,
//	        "subPath": f.name
//	      }) : [])
//	  : oc_omit()}
//
// Behavior:
//   - Processes all files from container.configs.files and container.secrets.files
//   - Generates volume mount entries with proper name, mountPath, and subPath
//   - Volume names use "file-mount-" prefix + hash of (mountPath+"/"+fileName)
//   - Returns empty array if no file configurations exist
//   - Returns empty array if container configuration is invalid
//
// Generated volumeMounts structure:
//   - name: "file-mount-{8-char-hash}"  # Hash of mountPath+"/"+fileName
//   - mountPath: "{f.mountPath}/{f.name}"  # Full path where file should be mounted
//   - subPath: "{f.name}"  # File name within the volume
//
// # volumes() - Generate Volumes Configuration
//
// Generates volumes entries for Kubernetes pod specifications based on file configurations.
// This function simplifies the creation of volumes by automatically processing both config files
// and secret files from the container's configurations and generating the appropriate configMap
// and secret volume entries.
//
// Usage: ${configurations["containerName"].volumes(metadataName)}
//
// Parameters:
//   - metadataName: The metadata name used for generating ConfigMap and Secret names
//
// Returns: An array of volumes entries for all files in configs.files and secrets.files
//
// Example usage:
//
//	# Simple usage in Deployment pod spec
//	volumes: ${configurations[parameters.containerName].volumes(metadata.name)}
//	
//	# Or with literal container name
//	volumes: ${configurations["main"].volumes(metadata.name)}
//
//	# Replaces complex manual logic:
//	volumes: |
//	  ${has(configurations[parameters.containerName].configs.files) && configurations[parameters.containerName].configs.files.size() > 0 || has(configurations[parameters.containerName].secrets.files) && configurations[parameters.containerName].secrets.files.size() > 0 ?
//	    (has(configurations[parameters.containerName].configs.files) && configurations[parameters.containerName].configs.files.size() > 0 ?
//	      configurations[parameters.containerName].configs.files.map(f, {
//	        "name": "file-mount-"+oc_hash(f.mountPath+"/"+f.name),
//	        "configMap": {
//	          "name": oc_generate_name(metadata.name, "config", f.name).replace(".", "-")
//	        }
//	      }) : []) +
//	     (has(configurations[parameters.containerName].secrets.files) && configurations[parameters.containerName].secrets.files.size() > 0 ?
//	      configurations[parameters.containerName].secrets.files.map(f, {
//	        "name": "file-mount-"+oc_hash(f.mountPath+"/"+f.name),
//	        "secret": {
//	          "secretName": oc_generate_name(metadata.name, "secret", f.name).replace(".", "-")
//	        }
//	      }) : [])
//	  : oc_omit()}
//
// Behavior:
//   - Processes all files from container.configs.files and container.secrets.files
//   - Generates configMap volume entries for config files
//   - Generates secret volume entries for secret files
//   - Volume names use "file-mount-" prefix + hash of (mountPath+"/"+fileName)
//   - ConfigMap/Secret names use oc_generate_name pattern with dots replaced by hyphens
//   - Returns empty array if no file configurations exist
//   - Returns empty array if container configuration is invalid
//
// Generated volumes structure:
//   - name: "file-mount-{8-char-hash}"  # Hash of mountPath+"/"+fileName
//   - configMap.name: "{metadataName}-config-{fileName}"  # For config files
//   - secret.secretName: "{metadataName}-secret-{fileName}"  # For secret files
//
// All custom functions use the "oc_" prefix to avoid potential conflicts with upstream CEL-go.
// The envFrom, volumeMounts, and volumes functions are exceptions as they're methods on the configurations object.
func CustomFunctions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Macros(generateNameMacro, mergeMacro),
		cel.Function("oc_omit",
			cel.Overload("oc_omit", []*cel.Type{}, cel.DynType,
				cel.FunctionBinding(func(values ...ref.Val) ref.Val {
					return omitCEL
				}),
			),
		),
		cel.Function("oc_merge",
			cel.Overload("oc_merge_map_map",
				[]*cel.Type{cel.MapType(cel.StringType, cel.DynType), cel.MapType(cel.StringType, cel.DynType)},
				cel.MapType(cel.StringType, cel.DynType),
				cel.BinaryBinding(mergeMapFunction),
			),
		),
		cel.Function("oc_generate_name",
			cel.Overload("oc_generate_name_string",
				[]*cel.Type{cel.StringType},
				cel.StringType,
				cel.UnaryBinding(func(arg ref.Val) ref.Val {
					return generateK8sNameFromStrings([]string{arg.Value().(string)})
				}),
			),
			cel.Overload("oc_generate_name_list",
				[]*cel.Type{cel.ListType(cel.StringType)},
				cel.StringType,
				cel.UnaryBinding(generateK8sName),
			),
		),
		cel.Function("oc_hash",
			cel.Overload("oc_hash_string", []*cel.Type{cel.StringType}, cel.StringType,
				cel.UnaryBinding(func(arg ref.Val) ref.Val {
					input := arg.Value().(string)
					h := fnv.New32a()
					h.Write([]byte(input))
					return types.String(fmt.Sprintf("%08x", h.Sum32()))
				}),
			),
		),
		cel.Function("envFrom",
			cel.MemberOverload("container_configurations_envFrom",
				[]*cel.Type{cel.MapType(cel.StringType, cel.DynType), cel.StringType},
				cel.ListType(cel.MapType(cel.StringType, cel.DynType)),
				cel.FunctionBinding(func(values ...ref.Val) ref.Val {
					return generateEnvFromForContainer(values[0], values[1])
				}),
			),
		),
		cel.Function("volumeMounts",
			cel.MemberOverload("container_configurations_volumeMounts",
				[]*cel.Type{cel.MapType(cel.StringType, cel.DynType)},
				cel.ListType(cel.MapType(cel.StringType, cel.DynType)),
				cel.FunctionBinding(func(values ...ref.Val) ref.Val {
					return generateVolumeMountsForContainer(values[0])
				}),
			),
		),
		cel.Function("volumes",
			cel.MemberOverload("container_configurations_volumes",
				[]*cel.Type{cel.MapType(cel.StringType, cel.DynType), cel.StringType},
				cel.ListType(cel.MapType(cel.StringType, cel.DynType)),
				cel.FunctionBinding(func(values ...ref.Val) ref.Val {
					return generateVolumesForContainer(values[0], values[1])
				}),
			),
		),
	}
}

// mergeMapFunction implements the binary oc_merge() CEL function.
//
// Performs a shallow merge of two maps, with values from rhs overriding values from lhs.
// Nested maps are replaced entirely, not merged recursively.
//
// The mergeMacro expands variadic calls into nested binary calls:
//   - oc_merge(a, b, c) → oc_merge(oc_merge(a, b), c)
//
// See CustomFunctions() for detailed usage examples.
func mergeMapFunction(lhs, rhs ref.Val) ref.Val {
	baseVal := lhs.Value()
	overrideVal := rhs.Value()

	baseMap := make(map[string]any)
	overrideMap := make(map[string]any)

	// Convert base map from CEL types to Go types
	switch b := baseVal.(type) {
	case map[string]any:
		baseMap = b
	case map[ref.Val]ref.Val:
		for k, v := range b {
			baseMap[string(k.(types.String))] = v.Value()
		}
	}

	// Convert override map from CEL types to Go types
	switch o := overrideVal.(type) {
	case map[string]any:
		overrideMap = o
	case map[ref.Val]ref.Val:
		for k, v := range o {
			overrideMap[string(k.(types.String))] = v.Value()
		}
	}

	// Merge maps
	result := make(map[string]any)
	maps.Copy(result, baseMap)
	maps.Copy(result, overrideMap)

	// Convert back to CEL map type
	celResult := make(map[ref.Val]ref.Val)
	for k, v := range result {
		celResult[types.String(k)] = types.DefaultTypeAdapter.NativeToValue(v)
	}

	return types.NewDynamicMap(types.DefaultTypeAdapter, celResult)
}

// generateK8sNameFromStrings generates a valid Kubernetes resource name from arbitrary strings.
//
// Sanitizes input to follow DNS subdomain rules (lowercase alphanumeric, hyphens, dots),
// truncates to 253 characters, and appends an 8-character hash suffix for uniqueness.
//
// See CustomFunctions() for detailed usage examples.
func generateK8sNameFromStrings(parts []string) ref.Val {
	result := kubernetes.GenerateK8sNameWithLengthLimit(kubernetes.MaxResourceNameLength, parts...)
	return types.String(result)
}

// generateK8sName is the CEL binding for oc_generate_name().
//
// Handles multiple input formats (single string, array, variadic via macro).
// Non-string list items are silently ignored, allowing mixed-type lists.
//
// See CustomFunctions() for detailed usage examples.
func generateK8sName(arg ref.Val) ref.Val {
	// CEL callers can hand us either a list (`["foo", "-", "bar"]`) or a dynamic list of ref.Val.
	// Accept all of them so reusable template helpers keep working unchanged.
	parts := []string{}

	// Handle different input types
	switch v := arg.Value().(type) {
	case string:
		parts = append(parts, v)
	case []ref.Val:
		for _, item := range v {
			if str, ok := item.Value().(string); ok {
				parts = append(parts, str)
			}
		}
	case []any:
		for _, item := range v {
			if str, ok := item.(string); ok {
				parts = append(parts, str)
			}
		}
	}

	return generateK8sNameFromStrings(parts)
}

// generateNameMacro enables variadic syntax for oc_generate_name in templates.
//
// This macro transforms variadic calls into list-based calls that the runtime function can handle:
//   - oc_generate_name("a", "b", "c") → oc_generate_name(["a", "b", "c"])
//   - oc_generate_name() → oc_generate_name([])
//   - oc_generate_name("single") → passes through unchanged (no macro expansion needed)
//
// This allows template authors to use natural syntax like ${oc_generate_name(component.name, "-", environment)}
// instead of the more verbose ${oc_generate_name([component.name, "-", environment])}.
var generateNameMacro = cel.GlobalVarArgMacro("oc_generate_name",
	func(eh parser.ExprHelper, target ast.Expr, args []ast.Expr) (ast.Expr, *common.Error) {
		switch len(args) {
		case 0:
			// No args: convert to empty list
			return eh.NewCall("oc_generate_name", eh.NewList()), nil
		case 1:
			// Single arg: no macro expansion needed, pass through to function
			return nil, nil
		default:
			// Multiple args: wrap in list for function to process
			return eh.NewCall("oc_generate_name", eh.NewList(args...)), nil
		}
	})

// mergeMacro enables variadic syntax for oc_merge in templates.
//
// This macro transforms variadic calls into nested binary calls that chain the merges:
//   - oc_merge(a, b) → passes through unchanged (binary function handles it)
//   - oc_merge(a, b, c) → oc_merge(oc_merge(a, b), c)
//   - oc_merge(a, b, c, d) → oc_merge(oc_merge(oc_merge(a, b), c), d)
//
// This allows template authors to merge multiple maps in a single call:
//
//	${oc_merge(defaults, component.spec, env.overrides)}
//
// The merge is left-associative, meaning later arguments override earlier ones.
var mergeMacro = cel.GlobalVarArgMacro("oc_merge",
	func(eh parser.ExprHelper, target ast.Expr, args []ast.Expr) (ast.Expr, *common.Error) {
		switch len(args) {
		case 0, 1:
			// Need at least 2 arguments for merge
			return nil, &common.Error{
				Message: "oc_merge requires at least 2 arguments",
			}
		case 2:
			// Binary call: no macro expansion needed, pass through to function
			return nil, nil
		default:
			// Variadic call: chain merges left-to-right
			// oc_merge(a, b, c, d) becomes oc_merge(oc_merge(oc_merge(a, b), c), d)
			result := eh.NewCall("oc_merge", args[0], args[1])
			for i := 2; i < len(args); i++ {
				result = eh.NewCall("oc_merge", result, args[i])
			}
			return result, nil
		}
	})

// generateEnvFromForContainer creates envFrom entries for configMaps and secrets based on container configurations.
//
// This function generates the envFrom array that can be used in Kubernetes container specifications
// to load environment variables from ConfigMaps and Secrets. It takes a single container's configurations
// and a metadata name to generate the appropriate configMapRef and secretRef entries.
//
// Usage: ${configurations["containerName"].envFrom(metadataName)}
//
// Parameters:
//   - containerConfigsVal: The container configuration map containing configs and secrets
//   - metadataNameVal: The metadata name used for generating resource names
//
// Returns: A list of envFrom entries with configMapRef and secretRef as needed
func generateEnvFromForContainer(containerConfigsVal, metadataNameVal ref.Val) ref.Val {
	// Convert CEL values to Go types
	metadataName := metadataNameVal.Value().(string)
	
	// Convert container configs to map
	containerConfigs := make(map[string]any)
	switch cc := containerConfigsVal.Value().(type) {
	case map[string]any:
		containerConfigs = cc
	case map[ref.Val]ref.Val:
		for k, v := range cc {
			containerConfigs[string(k.(types.String))] = v.Value()
		}
	default:
		return types.NewDynamicList(types.DefaultTypeAdapter, []ref.Val{})
	}
	
	var envFromEntries []ref.Val
	
	// Check for config envs
	if configs, hasConfigs := containerConfigs["configs"]; hasConfigs {
		if configsMap, ok := configs.(map[string]any); ok {
			if envs, hasEnvs := configsMap["envs"]; hasEnvs {
				// Check if envs list has items
				if envsList, ok := envs.([]any); ok && len(envsList) > 0 {
					// Create configMapRef entry
					configMapRef := map[ref.Val]ref.Val{
						types.String("configMapRef"): types.NewDynamicMap(types.DefaultTypeAdapter, map[ref.Val]ref.Val{
							types.String("name"): generateConfigMapName(metadataName),
						}),
					}
					envFromEntries = append(envFromEntries, types.NewDynamicMap(types.DefaultTypeAdapter, configMapRef))
				}
			}
		}
	}
	
	// Check for secret envs
	if secrets, hasSecrets := containerConfigs["secrets"]; hasSecrets {
		if secretsMap, ok := secrets.(map[string]any); ok {
			if envs, hasEnvs := secretsMap["envs"]; hasEnvs {
				// Check if envs list has items
				if envsList, ok := envs.([]any); ok && len(envsList) > 0 {
					// Create secretRef entry
					secretRef := map[ref.Val]ref.Val{
						types.String("secretRef"): types.NewDynamicMap(types.DefaultTypeAdapter, map[ref.Val]ref.Val{
							types.String("name"): generateSecretName(metadataName),
						}),
					}
					envFromEntries = append(envFromEntries, types.NewDynamicMap(types.DefaultTypeAdapter, secretRef))
				}
			}
		}
	}
	
	return types.NewDynamicList(types.DefaultTypeAdapter, envFromEntries)
}

// generateVolumeMountsForContainer creates volumeMounts entries for config and secret files.
//
// This function generates the volumeMounts array that can be used in Kubernetes container specifications
// to mount files from ConfigMaps and Secrets. It takes a single container's configurations
// and generates the appropriate volumeMounts entries for all files.
//
// Usage: ${configurations["containerName"].volumeMounts()}
//
// Parameters:
//   - containerConfigsVal: The container configuration map containing configs and secrets
//
// Returns: A list of volumeMounts entries for all config and secret files
func generateVolumeMountsForContainer(containerConfigsVal ref.Val) ref.Val {
	// Convert container configs to map
	containerConfigs := make(map[string]any)
	switch cc := containerConfigsVal.Value().(type) {
	case map[string]any:
		containerConfigs = cc
	case map[ref.Val]ref.Val:
		for k, v := range cc {
			containerConfigs[string(k.(types.String))] = v.Value()
		}
	default:
		return types.NewDynamicList(types.DefaultTypeAdapter, []ref.Val{})
	}
	
	var volumeMountEntries []ref.Val
	
	// Process config files
	if configs, hasConfigs := containerConfigs["configs"]; hasConfigs {
		if configsMap, ok := configs.(map[string]any); ok {
			if files, hasFiles := configsMap["files"]; hasFiles {
				if filesList, ok := files.([]any); ok {
					for _, fileAny := range filesList {
						if fileMap, ok := fileAny.(map[string]any); ok {
							volumeMount := generateVolumeMountEntry(fileMap)
							if volumeMount != nil {
								volumeMountEntries = append(volumeMountEntries, volumeMount)
							}
						}
					}
				}
			}
		}
	}
	
	// Process secret files
	if secrets, hasSecrets := containerConfigs["secrets"]; hasSecrets {
		if secretsMap, ok := secrets.(map[string]any); ok {
			if files, hasFiles := secretsMap["files"]; hasFiles {
				if filesList, ok := files.([]any); ok {
					for _, fileAny := range filesList {
						if fileMap, ok := fileAny.(map[string]any); ok {
							volumeMount := generateVolumeMountEntry(fileMap)
							if volumeMount != nil {
								volumeMountEntries = append(volumeMountEntries, volumeMount)
							}
						}
					}
				}
			}
		}
	}
	
	return types.NewDynamicList(types.DefaultTypeAdapter, volumeMountEntries)
}

// generateVolumeMountEntry creates a single volumeMount entry from a file configuration
func generateVolumeMountEntry(fileMap map[string]any) ref.Val {
	name, hasName := fileMap["name"]
	mountPath, hasMountPath := fileMap["mountPath"]
	
	if !hasName || !hasMountPath {
		return nil
	}
	
	nameStr, ok1 := name.(string)
	mountPathStr, ok2 := mountPath.(string)
	if !ok1 || !ok2 {
		return nil
	}
	
	// Generate volume name using hash of mountPath + "/" + fileName
	fullPath := mountPathStr + "/" + nameStr
	volumeName := "file-mount-" + generateHashString(fullPath)
	
	// Create volumeMount entry
	volumeMount := map[ref.Val]ref.Val{
		types.String("name"):      types.String(volumeName),
		types.String("mountPath"): types.String(fullPath),
		types.String("subPath"):   types.String(nameStr),
	}
	
	return types.NewDynamicMap(types.DefaultTypeAdapter, volumeMount)
}

// generateHashString generates an 8-character hash string like oc_hash does
func generateHashString(input string) string {
	h := fnv.New32a()
	h.Write([]byte(input))
	return fmt.Sprintf("%08x", h.Sum32())
}

// generateVolumesForContainer creates volumes entries for config and secret files.
//
// This function generates the volumes array that can be used in Kubernetes pod specifications
// to define volumes from ConfigMaps and Secrets. It takes a single container's configurations
// and generates the appropriate volumes entries for all files.
//
// Usage: ${configurations["containerName"].volumes(metadataName)}
//
// Parameters:
//   - containerConfigsVal: The container configuration map containing configs and secrets
//   - metadataNameVal: The metadata name used for generating resource names
//
// Returns: A list of volumes entries for all config and secret files
func generateVolumesForContainer(containerConfigsVal, metadataNameVal ref.Val) ref.Val {
	// Convert CEL values to Go types
	metadataName := metadataNameVal.Value().(string)
	
	// Convert container configs to map
	containerConfigs := make(map[string]any)
	switch cc := containerConfigsVal.Value().(type) {
	case map[string]any:
		containerConfigs = cc
	case map[ref.Val]ref.Val:
		for k, v := range cc {
			containerConfigs[string(k.(types.String))] = v.Value()
		}
	default:
		return types.NewDynamicList(types.DefaultTypeAdapter, []ref.Val{})
	}
	
	var volumeEntries []ref.Val
	
	// Process config files
	if configs, hasConfigs := containerConfigs["configs"]; hasConfigs {
		if configsMap, ok := configs.(map[string]any); ok {
			if files, hasFiles := configsMap["files"]; hasFiles {
				if filesList, ok := files.([]any); ok {
					for _, fileAny := range filesList {
						if fileMap, ok := fileAny.(map[string]any); ok {
							volume := generateConfigMapVolumeEntry(fileMap, metadataName)
							if volume != nil {
								volumeEntries = append(volumeEntries, volume)
							}
						}
					}
				}
			}
		}
	}
	
	// Process secret files
	if secrets, hasSecrets := containerConfigs["secrets"]; hasSecrets {
		if secretsMap, ok := secrets.(map[string]any); ok {
			if files, hasFiles := secretsMap["files"]; hasFiles {
				if filesList, ok := files.([]any); ok {
					for _, fileAny := range filesList {
						if fileMap, ok := fileAny.(map[string]any); ok {
							volume := generateSecretVolumeEntry(fileMap, metadataName)
							if volume != nil {
								volumeEntries = append(volumeEntries, volume)
							}
						}
					}
				}
			}
		}
	}
	
	return types.NewDynamicList(types.DefaultTypeAdapter, volumeEntries)
}

// generateConfigMapVolumeEntry creates a single configMap volume entry from a file configuration
func generateConfigMapVolumeEntry(fileMap map[string]any, metadataName string) ref.Val {
	name, hasName := fileMap["name"]
	mountPath, hasMountPath := fileMap["mountPath"]
	
	if !hasName || !hasMountPath {
		return nil
	}
	
	nameStr, ok1 := name.(string)
	mountPathStr, ok2 := mountPath.(string)
	if !ok1 || !ok2 {
		return nil
	}
	
	// Generate volume name using hash of mountPath + "/" + fileName
	fullPath := mountPathStr + "/" + nameStr
	volumeName := "file-mount-" + generateHashString(fullPath)
	
	// Generate configMap name using oc_generate_name pattern
	configMapName := generateK8sNameFromStrings([]string{metadataName, "config", nameStr})
	// Replace dots with hyphens as shown in the original logic
	configMapNameStr := configMapName.Value().(string)
	configMapNameStr = strings.ReplaceAll(configMapNameStr, ".", "-")
	
	// Create volume entry
	volume := map[ref.Val]ref.Val{
		types.String("name"): types.String(volumeName),
		types.String("configMap"): types.NewDynamicMap(types.DefaultTypeAdapter, map[ref.Val]ref.Val{
			types.String("name"): types.String(configMapNameStr),
		}),
	}
	
	return types.NewDynamicMap(types.DefaultTypeAdapter, volume)
}

// generateSecretVolumeEntry creates a single secret volume entry from a file configuration
func generateSecretVolumeEntry(fileMap map[string]any, metadataName string) ref.Val {
	name, hasName := fileMap["name"]
	mountPath, hasMountPath := fileMap["mountPath"]
	
	if !hasName || !hasMountPath {
		return nil
	}
	
	nameStr, ok1 := name.(string)
	mountPathStr, ok2 := mountPath.(string)
	if !ok1 || !ok2 {
		return nil
	}
	
	// Generate volume name using hash of mountPath + "/" + fileName
	fullPath := mountPathStr + "/" + nameStr
	volumeName := "file-mount-" + generateHashString(fullPath)
	
	// Generate secret name using oc_generate_name pattern
	secretName := generateK8sNameFromStrings([]string{metadataName, "secret", nameStr})
	// Replace dots with hyphens as shown in the original logic
	secretNameStr := secretName.Value().(string)
	secretNameStr = strings.ReplaceAll(secretNameStr, ".", "-")
	
	// Create volume entry
	volume := map[ref.Val]ref.Val{
		types.String("name"): types.String(volumeName),
		types.String("secret"): types.NewDynamicMap(types.DefaultTypeAdapter, map[ref.Val]ref.Val{
			types.String("secretName"): types.String(secretNameStr),
		}),
	}
	
	return types.NewDynamicMap(types.DefaultTypeAdapter, volume)
}

// generateConfigMapName generates a ConfigMap name using the same pattern as oc_generate_name
func generateConfigMapName(metadataName string) ref.Val {
	result := generateK8sNameFromStrings([]string{metadataName, "env-configs"})
	return result
}

// generateSecretName generates a Secret name using the same pattern as oc_generate_name  
func generateSecretName(metadataName string) ref.Val {
	result := generateK8sNameFromStrings([]string{metadataName, "env-secrets"})
	return result
}
