// Copyright 2025 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package patch

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
)

// filterExpr recognises the `[?(@.field=='value')]` selectors used in array filter expressions.
// The pattern captures the field path (group 1) and the expected value (group 2).
// Example: `[?(@.name=='app')]` matches items where the 'name' field equals 'app'.
var filterExpr = regexp.MustCompile(`^@\.([A-Za-z0-9_.-]+)\s*==\s*['"](.*)['"]$`)

const opRemove = "remove"

// RenderFunc evaluates CEL expressions within template values.
// It takes a value (which may contain ${...} expressions) and a map of input variables,
// and returns the rendered result with all CEL expressions evaluated.
type RenderFunc func(value any, inputs map[string]any) (any, error)

// MissingDataChecker determines if an error represents missing/unavailable template data.
// This allows patch operations to gracefully skip resources when data is not yet available,
// rather than failing the entire patch operation.
type MissingDataChecker func(err error) bool

// ApplyOperation applies a single patch operation against a target resource.
//
// The function first evaluates the path and value using the provided render function,
// which handles any CEL expressions like ${...}. It then routes the operation to the
// appropriate handler based on the operation type.
//
// Supported operations:
//   - RFC 6902 ops (add, replace, remove, test, move, copy): standard JSON Patch
//   - mergeShallow: custom operation that overlays map keys without deep merging
func ApplyOperation(target map[string]any, operation JSONPatchOperation, inputs map[string]any, render RenderFunc) error {
	// Evaluate the path, which may contain CEL expressions
	pathValue, err := render(operation.Path, inputs)
	if err != nil {
		return fmt.Errorf("failed to evaluate patch path: %w", err)
	}

	pathStr, ok := pathValue.(string)
	if !ok {
		return fmt.Errorf("patch path must evaluate to a string, got %T", pathValue)
	}

	// Evaluate the value, unless this is a remove operation
	var value any
	if operation.Op != opRemove {
		value, err = render(operation.Value, inputs)
		if err != nil {
			return fmt.Errorf("failed to evaluate patch value: %w", err)
		}
	}

	// Route to the appropriate operation handler
	op := strings.ToLower(operation.Op)
	switch op {
	case "add", "replace", opRemove, "test", "move", "copy":
		return applyRFC6902(target, op, pathStr, value)
	case "mergeshallow":
		return applyMergeShallow(target, pathStr, value)
	default:
		return fmt.Errorf("unknown patch operation: %s", operation.Op)
	}
}

// applyRFC6902 executes standard JSON Patch operations after expanding the path.
//
// Path expansion allows a single operation to target multiple locations:
//   - /containers[?(@.name=='app')]/image targets all matching containers
//   - /env/- appends to an array
//
// For "add" operations, we ensure parent containers exist before applying the patch.
// If the expanded path resolves to zero locations (e.g., filter matched nothing),
// the operation is treated as a no-op rather than an error.
func applyRFC6902(target map[string]any, op, rawPath string, value any) error {
	// Expand paths to handle filters and special markers
	resolved, err := expandPaths(target, rawPath)
	if err != nil {
		return err
	}
	if len(resolved) == 0 {
		// No matches (e.g., filter didn't match anything); treat as no-op.
		return nil
	}

	// Apply the operation to each resolved location
	for _, pointer := range resolved {
		if op == "add" {
			// Create missing parent containers for add operations
			if err := ensureParentExists(target, pointer); err != nil {
				return err
			}
		}
		if err := applyJSONPatch(target, op, pointer, value); err != nil {
			return err
		}
	}
	return nil
}

// applyMergeShallow applies a shallow merge operation, overlaying top-level keys
// without recursively merging nested structures.
//
// Unlike standard merge (or strategic merge patch), mergeShallow replaces entire
// nested objects rather than deep merging them. This gives more predictable behavior
// when you want to replace a nested configuration block completely.
//
// Example:
//   existing: {a: {x: 1, y: 2}, b: 3}
//   overlay:  {a: {z: 3}}
//   result:   {a: {z: 3}, b: 3}  // note: a.x and a.y are gone
func applyMergeShallow(target map[string]any, rawPath string, value any) error {
	valueMap, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("mergeShallow value must be an object")
	}

	resolved, err := expandPaths(target, rawPath)
	if err != nil {
		return err
	}
	if len(resolved) == 0 {
		// Nothing to merge into.
		return nil
	}

	for _, pointer := range resolved {
		if err := mergeShallowAtPointer(target, pointer, valueMap); err != nil {
			return err
		}
	}
	return nil
}

// --- Path expansion --------------------------------------------------------

// pathState represents a single location within the document tree during path expansion.
// As we traverse the path, we maintain both the JSON Pointer segments and the actual
// value at that location, allowing us to evaluate filters and determine valid next steps.
type pathState struct {
	pointer []string // JSON Pointer segments (without leading "/" or escaping applied)
	value   any      // The value at this location in the document
}

// expandPaths converts a path expression into one or more JSON Pointers.
//
// Path expressions extend standard JSON Pointer with:
//   - Array filters: /containers[?(@.name=='app')]/env
//   - Array indices: /containers/0/env
//   - Append marker: /env/-
//
// A single path can expand to multiple JSON Pointers when filters match multiple elements.
// For example, /containers[?(@.role=='worker')]/image might expand to:
//   - /containers/0/image
//   - /containers/2/image
//   - /containers/5/image
//
// The algorithm maintains a set of possible states as it processes each segment,
// allowing filters to fan out into multiple parallel paths.
func expandPaths(root map[string]any, rawPath string) ([]string, error) {
	if rawPath == "" {
		return []string{""}, nil
	}

	segments := splitRawPath(rawPath)
	// Start with a single state representing the root
	states := []pathState{{pointer: []string{}, value: root}}

	// Process each segment, potentially expanding to multiple states
	for _, segment := range segments {
		// Handle the append marker specially (doesn't need the current value)
		if segment == "-" {
			states = applyDash(states)
			continue
		}

		// Expand each current state by applying this segment
		nextStates := make([]pathState, 0, len(states))
		for _, st := range states {
			expanded, err := applySegment(st, segment)
			if err != nil {
				return nil, err
			}
			nextStates = append(nextStates, expanded...)
		}
		states = nextStates

		// If we have no states, a filter matched nothing or a path was invalid
		if len(states) == 0 {
			break
		}
	}

	// Convert final states to JSON Pointers
	pointers := make([]string, 0, len(states))
	for _, st := range states {
		pointers = append(pointers, buildJSONPointer(st.pointer))
	}
	return pointers, nil
}

// applySegment processes a single path segment, which may contain multiple sub-parts.
//
// Segments can be complex expressions like:
//   - "containers" (simple key)
//   - "0" (numeric index)
//   - "[0]" (bracketed index)
//   - "[?(@.name=='app')]" (filter)
//   - "containers[0]" (key followed by index)
//   - "[?(@.role=='worker')][0]" (filter followed by index)
//
// The function iteratively parses these sub-parts rather than using simple splitting,
// because brackets may be nested or combined in complex ways.
//
// Returns a slice of states representing all possible locations after traversing this segment.
func applySegment(state pathState, segment string) ([]pathState, error) {
	current := []pathState{state}
	remaining := segment

	// Parse the segment character by character, handling brackets specially
	for len(remaining) > 0 {
		if strings.HasPrefix(remaining, "[") {
			// Extract bracket content: [...]
			closeIdx := strings.Index(remaining, "]")
			if closeIdx == -1 {
				return nil, fmt.Errorf("unclosed bracket segment in %q", segment)
			}
			content := remaining[1:closeIdx]
			remaining = remaining[closeIdx+1:]

			// Determine bracket type and apply appropriate operation
			var err error
			switch {
			case strings.HasPrefix(content, "?(") && strings.HasSuffix(content, ")"):
				// Array filter: [?(@.field=='value')]
				expr := content[2 : len(content)-1]
				current, err = applyFilter(current, expr)
			case content == "-":
				// Append marker: [-]
				current = applyDash(current)
			default:
				// Numeric index: [0], [1], etc.
				index, parseErr := strconv.Atoi(content)
				if parseErr != nil {
					return nil, fmt.Errorf("unsupported array index %q", content)
				}
				current, err = applyIndex(current, index)
			}
			if err != nil {
				return nil, err
			}
		} else {
			// Non-bracket content: parse until the next bracket or end
			nextBracket := strings.Index(remaining, "[")
			var token string
			if nextBracket == -1 {
				token = remaining
				remaining = ""
			} else {
				token = remaining[:nextBracket]
				remaining = remaining[nextBracket:]
			}
			if token == "" {
				continue
			}

			// Token could be a bare number (array index) or a key
			if idx, err := strconv.Atoi(token); err == nil {
				current, err = applyIndex(current, idx)
				if err != nil {
					return nil, err
				}
			} else {
				var err error
				current, err = applyKey(current, token)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	return current, nil
}

// applyKey traverses an object key for all current states.
// Each state should have an object value; we extract the specified key and create new states.
func applyKey(states []pathState, key string) ([]pathState, error) {
	if key == "" {
		return states, nil
	}

	next := make([]pathState, 0, len(states))
	for _, st := range states {
		var child any
		switch current := st.value.(type) {
		case map[string]any:
			child = current[key]
		case nil:
			// Traversing through nil is allowed; the child will also be nil
			child = nil
		default:
			return nil, fmt.Errorf("path segment %q expects an object, got %T", key, st.value)
		}
		next = append(next, pathState{
			pointer: appendPointer(st.pointer, key),
			value:   child,
		})
	}
	return next, nil
}

// applyIndex traverses an array index for all current states.
// Each state should have an array value; we extract the element at the specified index.
func applyIndex(states []pathState, index int) ([]pathState, error) {
	next := make([]pathState, 0, len(states))
	for _, st := range states {
		arr, ok := st.value.([]any)
		if !ok {
			return nil, fmt.Errorf("path segment expects an array, got %T", st.value)
		}
		if index < 0 || index >= len(arr) {
			return nil, fmt.Errorf("array index %d out of bounds", index)
		}
		next = append(next, pathState{
			pointer: appendPointer(st.pointer, strconv.Itoa(index)),
			value:   arr[index],
		})
	}
	return next, nil
}

// applyDash adds the array append marker "-" to all current states.
// The value is set to nil since "-" doesn't point to an existing element.
func applyDash(states []pathState) []pathState {
	next := make([]pathState, len(states))
	for i, st := range states {
		next[i] = pathState{
			pointer: appendPointer(st.pointer, "-"),
			value:   nil,
		}
	}
	return next
}

// applyFilter evaluates a filter expression against array elements.
//
// For each state that contains an array, we iterate through its elements
// and test each one against the filter. Elements that match become new states.
//
// This allows a single filter to fan out into multiple paths. For example,
// if containers = [{name: "app"}, {name: "sidecar"}, {name: "app"}],
// then [?(@.name=='app')] produces two states: [0] and [2].
//
// Note: Filters are evaluated using simple field lookups, not CEL, for simplicity.
func applyFilter(states []pathState, expr string) ([]pathState, error) {
	next := []pathState{}
	for _, st := range states {
		arr, ok := st.value.([]any)
		if !ok || len(arr) == 0 {
			// Not an array or empty array; skip this state
			continue
		}
		for idx, item := range arr {
			match, err := matchesFilter(item, expr)
			if err != nil {
				return nil, err
			}
			if match {
				next = append(next, pathState{
					pointer: appendPointer(st.pointer, strconv.Itoa(idx)),
					value:   item,
				})
			}
		}
	}
	return next, nil
}

// matchesFilter tests if an item matches a filter expression.
//
// Currently supports only equality filters of the form: @.field.path=='value'
// The field path can contain dots for nested fields: @.metadata.labels.app=='web'
//
// Returns false (without error) if the field path doesn't exist or types don't match.
func matchesFilter(item any, expr string) (bool, error) {
	matches := filterExpr.FindStringSubmatch(strings.TrimSpace(expr))
	if len(matches) != 3 {
		return false, fmt.Errorf("unsupported filter expression: %s", expr)
	}

	fieldPath := strings.Split(matches[1], ".")
	expected := matches[2]

	// Navigate through nested fields
	current := item
	for _, segment := range fieldPath {
		m, ok := current.(map[string]any)
		if !ok {
			// Field path expects an object but got something else
			return false, nil
		}
		current, ok = m[segment]
		if !ok {
			// Field doesn't exist
			return false, nil
		}
	}

	// Compare the final value
	if current == nil {
		return expected == "", nil
	}
	return fmt.Sprintf("%v", current) == expected, nil
}

// splitRawPath splits a path expression into segments.
// Handles both "/foo/bar" and "foo/bar" formats (leading slash is optional).
func splitRawPath(path string) []string {
	if path == "" {
		return []string{}
	}
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return []string{""}
	}
	return strings.Split(trimmed, "/")
}

// appendPointer creates a new pointer slice with an additional segment.
// This preserves immutability of the original pointer.
func appendPointer(base []string, segment string) []string {
	next := make([]string, len(base)+1)
	copy(next, base)
	next[len(base)] = segment
	return next
}

// buildJSONPointer converts pointer segments into a proper RFC 6901 JSON Pointer string.
//
// Each segment is prefixed with "/" and escaped according to RFC 6901:
//   - "~" becomes "~0"
//   - "/" becomes "~1"
//
// The append marker "-" is not escaped since it has special meaning in JSON Pointer.
func buildJSONPointer(segments []string) string {
	if len(segments) == 0 {
		return ""
	}
	var b strings.Builder
	for _, seg := range segments {
		b.WriteByte('/')
		if seg == "-" {
			// Don't escape the append marker
			b.WriteString(seg)
		} else {
			b.WriteString(escapePointerSegment(seg))
		}
	}
	return b.String()
}

// --- RFC6902 execution -----------------------------------------------------

// applyJSONPatch applies a single RFC 6902 JSON Patch operation to the target document.
//
// This function delegates to github.com/evanphx/json-patch for the actual patching,
// which ensures correct RFC 6902 semantics. We marshal the target to JSON, apply the
// patch, then unmarshal back and update the target in-place.
//
// This approach ensures compatibility with the standard but requires serialization overhead.
func applyJSONPatch(target map[string]any, op, pointer string, value any) error {
	// Build a JSON Patch document (array with one operation)
	ops := []map[string]any{
		{
			"op":   op,
			"path": pointer,
		},
	}
	if op != opRemove {
		ops[0]["value"] = value
	}

	patchBytes, err := json.Marshal(ops)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	docBytes, err := json.Marshal(target)
	if err != nil {
		return fmt.Errorf("failed to marshal resource: %w", err)
	}

	patch, err := jsonpatch.DecodePatch(patchBytes)
	if err != nil {
		return fmt.Errorf("failed to decode JSON patch: %w", err)
	}

	patched, err := patch.Apply(docBytes)
	if err != nil {
		return fmt.Errorf("failed to apply JSON patch: %w", err)
	}

	var updated map[string]any
	if err := json.Unmarshal(patched, &updated); err != nil {
		return fmt.Errorf("failed to unmarshal patched document: %w", err)
	}

	// Update target in-place by clearing and copying
	for k := range target {
		delete(target, k)
	}
	for k, v := range updated {
		target[k] = v
	}
	return nil
}

// ensureParentExists creates intermediate containers along a path as needed.
//
// For "add" operations, we want to auto-create missing parent objects/arrays
// so patch authors don't need to manually check for existence. This function
// traverses all parent segments (everything except the final one) and creates
// containers where needed.
//
// Container type is determined by inspecting the next segment:
//   - If next is "-", create an empty array (for append operations)
//   - If next is a number, we CANNOT auto-create - return error
//   - Otherwise, create an empty object
//
// The restriction on numeric indices prevents ambiguity: if we're adding to
// /spec/containers/0/env and containers doesn't exist, how many elements should
// the array have? We can't know, so we require the array to already exist.
func ensureParentExists(root map[string]any, pointer string) error {
	segments := splitPointer(pointer)
	if len(segments) == 0 {
		return nil
	}

	// Traverse all parent segments (not including the final one)
	current := any(root)
	for i := 0; i < len(segments)-1; i++ {
		seg := segments[i]

		switch node := current.(type) {
		case map[string]any:
			child, exists := node[seg]
			if !exists || child == nil {
				// Determine what type of container to create
				next := segments[i+1]
				if next == "-" {
					// Next operation is append, create empty array
					node[seg] = []any{}
				} else if _, err := strconv.Atoi(next); err == nil {
					// Next operation needs a specific array index, but we can't
					// auto-create an array with that index - return error
					return fmt.Errorf("array index %s out of bounds at segment %s", next, seg)
				} else {
					// Next operation needs an object key, create empty object
					node[seg] = map[string]any{}
				}
				child = node[seg]
			}
			current = child
		case []any:
			// Current segment should be an array index
			index, err := strconv.Atoi(seg)
			if err != nil {
				return fmt.Errorf("expected array index at segment %s", seg)
			}
			if index < 0 || index >= len(node) {
				return fmt.Errorf("array index %d out of bounds at segment %s", index, seg)
			}
			current = node[index]
		default:
			return fmt.Errorf("cannot traverse segment %s on type %T", seg, current)
		}
	}
	return nil
}

// --- Merge -----------------------------------------------------------------

// mergeShallowAtPointer performs a shallow merge at the location specified by the pointer.
//
// The merge behavior:
//   - If the target location doesn't exist or is nil, set it to a copy of value
//   - If the target location is not a map, replace it with a copy of value
//   - If the target location is a map, overlay value's keys onto it (shallow merge)
//
// Shallow merge means we copy top-level keys from value, but don't recursively merge
// nested structures. If both target and value have a key "nested" that contains an object,
// value's "nested" object completely replaces target's "nested" object.
func mergeShallowAtPointer(root map[string]any, pointer string, value map[string]any) error {
	parent, last, err := navigateToParent(root, pointer, true)
	if err != nil {
		return err
	}

	switch container := parent.(type) {
	case map[string]any:
		existing, exists := container[last]
		if !exists || existing == nil {
			// Target doesn't exist, set it to a copy of value
			container[last] = deepCopyMap(value)
			return nil
		}
		targetMap, ok := existing.(map[string]any)
		if !ok || targetMap == nil {
			// Target exists but isn't a map, replace it
			container[last] = deepCopyMap(value)
			return nil
		}
		// Target is a map, perform shallow merge
		mergeShallowInto(targetMap, value)
	case []any:
		if last == "-" {
			return fmt.Errorf("mergeShallow operation cannot target append position '-'")
		}
		index, err := strconv.Atoi(last)
		if err != nil {
			return fmt.Errorf("invalid array index %q for mergeShallow", last)
		}
		if index < 0 || index >= len(container) {
			return fmt.Errorf("array index %d out of bounds for mergeShallow", index)
		}
		existing := container[index]
		if existing == nil {
			container[index] = deepCopyMap(value)
			return nil
		}
		targetMap, ok := existing.(map[string]any)
		if !ok || targetMap == nil {
			container[index] = deepCopyMap(value)
			return nil
		}
		mergeShallowInto(targetMap, value)
	default:
		return fmt.Errorf("mergeShallow parent must be object or array, got %T", parent)
	}
	return nil
}

// mergeShallowInto overlays overlay's keys onto target, modifying target in-place.
// Values are cloned to avoid sharing references between the overlay and target.
func mergeShallowInto(target map[string]any, overlay map[string]any) {
	for k, v := range overlay {
		target[k] = cloneValue(v)
	}
}

// navigateToParent traverses all but the last segment of a pointer, returning the
// parent container and the final segment name.
//
// If create is true, missing intermediate containers are auto-created using the
// same logic as ensureParentExists.
//
// Returns: (parent container, final segment name, error)
func navigateToParent(root map[string]any, pointer string, create bool) (any, string, error) {
	segments := splitPointer(pointer)
	if len(segments) == 0 {
		return root, "", nil
	}
	parentSegs := segments[:len(segments)-1]
	last := segments[len(segments)-1]

	current := any(root)
	for i, seg := range parentSegs {
		switch node := current.(type) {
		case map[string]any:
			child, exists := node[seg]
			if !exists || child == nil {
				if !create {
					return nil, "", fmt.Errorf("missing path at segment %s", seg)
				}
				// Auto-create the missing container
				next := determineNextContainerType(parentSegs, i, last)
				node[seg] = next
				child = node[seg]
			}
			current = child
		case []any:
			index, err := strconv.Atoi(seg)
			if err != nil {
				return nil, "", fmt.Errorf("expected array index at segment %s", seg)
			}
			if index < 0 || index >= len(node) {
				return nil, "", fmt.Errorf("array index %d out of bounds at segment %s", index, seg)
			}
			current = node[index]
		default:
			return nil, "", fmt.Errorf("cannot traverse segment %s on type %T", seg, node)
		}
	}
	return current, last, nil
}

// determineNextContainerType decides what type of container to create by inspecting
// the next segment in the path.
//
// Logic:
//   - If next segment is "-" → create empty array (for append)
//   - If next segment is numeric → create empty array (for indexed access)
//   - Otherwise → create empty object (for key access)
func determineNextContainerType(segments []string, index int, last string) any {
	nextSeg := last
	if index+1 < len(segments) {
		nextSeg = segments[index+1]
	}
	if nextSeg == "-" {
		return []any{}
	}
	if _, err := strconv.Atoi(nextSeg); err == nil {
		return []any{}
	}
	return map[string]any{}
}

// --- Helpers ----------------------------------------------------------------

// splitPointer parses a JSON Pointer string into segments, unescaping each one.
//
// RFC 6901 escaping rules:
//   - "~0" represents "~"
//   - "~1" represents "/"
//
// The append marker "-" is not unescaped since it's a special token.
func splitPointer(pointer string) []string {
	if pointer == "" {
		return []string{}
	}
	trimmed := strings.TrimPrefix(pointer, "/")
	if trimmed == "" {
		return []string{""}
	}
	parts := strings.Split(trimmed, "/")
	for i, part := range parts {
		if part != "-" {
			parts[i] = unescapePointerSegment(part)
		}
	}
	return parts
}

// escapePointerSegment encodes a segment according to RFC 6901.
// Must escape "~" first, then "/", to avoid double-escaping.
func escapePointerSegment(seg string) string {
	seg = strings.ReplaceAll(seg, "~", "~0")
	seg = strings.ReplaceAll(seg, "/", "~1")
	return seg
}

// unescapePointerSegment decodes a segment according to RFC 6901.
// Must unescape "/" first, then "~", to correctly reverse the encoding.
func unescapePointerSegment(seg string) string {
	seg = strings.ReplaceAll(seg, "~1", "/")
	seg = strings.ReplaceAll(seg, "~0", "~")
	return seg
}

// cloneValue creates a deep copy of a value.
// Recursively copies maps and slices; other types are returned as-is.
func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return deepCopyMap(typed)
	case []any:
		return deepCopySlice(typed)
	default:
		// Primitives and other types are immutable or not deeply copied
		return typed
	}
}

// deepCopyMap recursively copies a map and all its nested maps/slices.
func deepCopyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	result := make(map[string]any, len(src))
	for k, v := range src {
		switch typed := v.(type) {
		case map[string]any:
			result[k] = deepCopyMap(typed)
		case []any:
			result[k] = deepCopySlice(typed)
		default:
			result[k] = typed
		}
	}
	return result
}

// deepCopySlice recursively copies a slice and all its nested maps/slices.
func deepCopySlice(src []any) []any {
	if src == nil {
		return nil
	}
	result := make([]any, len(src))
	for i, v := range src {
		switch typed := v.(type) {
		case map[string]any:
			result[i] = deepCopyMap(typed)
		case []any:
			result[i] = deepCopySlice(typed)
		default:
			result[i] = typed
		}
	}
	return result
}

// ApplySpec executes a complete patch specification against a set of resources.
//
// The function handles:
//   - Resource targeting based on Kind/Group/Version/Name
//   - Optional forEach iteration over a list of items
//   - Conditional filtering with target.where expressions
//   - Multiple operations per target
//   - Variable binding (resource, and forEach variable)
//
// Execution flow:
//  1. Find target resources matching the spec.Target criteria
//  2. If spec.ForEach is set, iterate over items and bind each to a variable
//  3. For each iteration (or once if no forEach), filter targets using spec.Target.Where
//  4. Apply all operations to matching targets
//
// Variable binding:
//   - "resource" is automatically bound to the current target during where evaluation and operations
//   - forEach variable (default name "item") is bound to the current iteration value
//
// Error handling:
//   - If isMissingData returns true for an error during where evaluation, the target is skipped
//   - Other errors during where evaluation or operation application are returned immediately
func ApplySpec(
	resources []map[string]any,
	spec PatchSpec,
	inputs map[string]any,
	matcher Matcher,
	render RenderFunc,
	isMissingData MissingDataChecker,
) error {
	targets := FindTargetResources(resources, spec.Target, matcher)

	if len(spec.Operations) == 0 {
		return nil
	}

	// matchTarget evaluates the target.where condition against a resource.
	// Temporarily binds the resource to the "resource" variable for CEL evaluation.
	matchTarget := func(where string, target map[string]any, baseInputs map[string]any) (bool, error) {
		if where == "" {
			// No where clause means always match
			return true, nil
		}

		// Bind the current resource for evaluation
		previous, had := baseInputs["resource"]
		baseInputs["resource"] = target

		result, err := render(where, baseInputs)

		// Restore previous resource binding
		if had {
			baseInputs["resource"] = previous
		} else {
			delete(baseInputs, "resource")
		}

		if err != nil {
			// If this is a "missing data" error, treat as non-match rather than failure
			if isMissingData != nil && isMissingData(err) {
				return false, nil
			}
			return false, fmt.Errorf("failed to evaluate target.where: %w", err)
		}

		boolResult, ok := result.(bool)
		if !ok {
			return false, fmt.Errorf("target.where must evaluate to a boolean, got %T", result)
		}
		return boolResult, nil
	}

	// executeOperations runs all operations against a single target.
	// Binds the target to "resource" during execution so operations can reference it.
	executeOperations := func(target map[string]any, baseInputs map[string]any) error {
		previous, had := baseInputs["resource"]
		baseInputs["resource"] = target

		for _, op := range spec.Operations {
			if err := ApplyOperation(target, op, baseInputs, render); err != nil {
				// Restore resource binding before returning error
				if had {
					baseInputs["resource"] = previous
				} else {
					delete(baseInputs, "resource")
				}
				return err
			}
		}

		// Restore resource binding after all operations
		if had {
			baseInputs["resource"] = previous
		} else {
			delete(baseInputs, "resource")
		}
		return nil
	}

	// Handle forEach iteration if specified
	if spec.ForEach != "" {
		// Evaluate the forEach expression to get the list of items
		itemsRaw, err := render(spec.ForEach, inputs)
		if err != nil {
			return fmt.Errorf("failed to evaluate patch forEach expression: %w", err)
		}

		items, ok := itemsRaw.([]any)
		if !ok {
			return fmt.Errorf("forEach expression must evaluate to an array, got %T", itemsRaw)
		}

		// Determine the variable name for each iteration (defaults to "item")
		varName := spec.Var
		if varName == "" {
			varName = "item"
		}

		// Save any existing value for this variable to restore later
		previous, hadVar := inputs[varName]

		// Iterate through each item
		for _, item := range items {
			// Bind the current item to the variable
			inputs[varName] = item

			// Apply operations to all matching targets for this item
			for _, target := range targets {
				match, err := matchTarget(spec.Target.Where, target, inputs)
				if err != nil {
					// Restore variable binding before returning
					if hadVar {
						inputs[varName] = previous
					} else {
						delete(inputs, varName)
					}
					return err
				}
				if !match {
					continue
				}
				if err := executeOperations(target, inputs); err != nil {
					// Restore variable binding before returning
					if hadVar {
						inputs[varName] = previous
					} else {
						delete(inputs, varName)
					}
					return err
				}
			}
		}

		// Restore the original variable binding
		if hadVar {
			inputs[varName] = previous
		} else {
			delete(inputs, varName)
		}
		return nil
	}

	// No forEach - apply operations once to all matching targets
	for _, target := range targets {
		match, err := matchTarget(spec.Target.Where, target, inputs)
		if err != nil {
			return err
		}
		if !match {
			continue
		}
		if err := executeOperations(target, inputs); err != nil {
			return err
		}
	}

	return nil
}

// FindTargetResources filters resources based on Kind, Group, Version, and Name.
//
// Matching is done in order:
//  1. If target.Kind is set, resource.kind must match
//  2. If target.Group is set, the group portion of resource.apiVersion must match
//  3. If target.Version is set, the version portion of resource.apiVersion must match
//  4. If target.Name is set, resource.metadata.name must match
//
// An empty field in the target spec means "match any value".
//
// apiVersion is split into group/version:
//   - "apps/v1" → group="apps", version="v1"
//   - "v1" → group="", version="v1" (core API)
func FindTargetResources(resources []map[string]any, target TargetSpec, selector Matcher) []map[string]any {
	matches := make([]map[string]any, 0, len(resources))
	for _, resource := range resources {
		// Match Kind
		if target.Kind != "" {
			if kind, ok := resource["kind"].(string); !ok || kind != target.Kind {
				continue
			}
		}

		// Match Group and Version (extracted from apiVersion)
		group := ""
		version := ""
		if gv, ok := resource["apiVersion"].(string); ok {
			group, version = splitAPIVersion(gv)
		}
		if target.Group != "" && group != target.Group {
			continue
		}
		if target.Version != "" && version != target.Version {
			continue
		}

		// Match Name (from metadata)
		if target.Name != "" {
			metadata, _ := resource["metadata"].(map[string]any)
			if metadata == nil || metadata["name"] != target.Name {
				continue
			}
		}

		matches = append(matches, resource)
	}
	return matches
}

// Matcher evaluates if a resource satisfies a selector expression.
// This is a function type allowing custom selection logic to be plugged in.
type Matcher func(resource map[string]any, selector string) bool

// splitAPIVersion separates a Kubernetes apiVersion into group and version parts.
//
// Examples:
//   - "apps/v1" → ("apps", "v1")
//   - "v1" → ("", "v1")         // Core API group
//   - "" → ("", "")
func splitAPIVersion(apiVersion string) (group, version string) {
	if apiVersion == "" {
		return "", ""
	}
	if strings.Contains(apiVersion, "/") {
		parts := strings.SplitN(apiVersion, "/", 2)
		return parts[0], parts[1]
	}
	// No slash means it's a core API (e.g., "v1")
	return "", apiVersion
}
