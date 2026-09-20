package core

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

func ComposeMise(shared, selected []byte, selectedIdentity string, resolver MergeResolver) (MiseMergeResult, error) {
	sharedConfig, err := ExtractMiseConfig("shared", shared)
	if err != nil {
		return MiseMergeResult{}, err
	}
	selectedConfig, err := ExtractMiseConfig(selectedIdentity, selected)
	if err != nil {
		return MiseMergeResult{}, err
	}
	return MergeMiseConfigs(sharedConfig, selectedConfig, selectedIdentity, resolver)
}

func MergeMiseConfigs(shared, selected MiseConfig, selectedIdentity string, resolver MergeResolver) (MiseMergeResult, error) {
	selectedRoot, environment, err := mergeMiseEnvironment(shared, selected, selectedIdentity, resolver)
	if err != nil {
		return MiseMergeResult{}, err
	}
	selected, err = extractMiseRoot(selectedIdentity, selectedRoot)
	if err != nil {
		return MiseMergeResult{}, err
	}

	root := make(map[string]any)
	keys := sortedUnionKeys(shared.Root, selected.Root)
	for _, key := range keys {
		if key == "tools" || key == "tasks" || key == "env" {
			continue
		}
		left, leftOK := shared.Root[key]
		right, rightOK := selected.Root[key]
		value, ok, err := mergeMiseValue(key, left, leftOK, right, rightOK, resolver)
		if err != nil {
			return MiseMergeResult{}, err
		}
		if ok {
			root[key] = value
		}
	}

	tools, err := mergeMiseTools(shared.Tools, selected.Tools, resolver)
	if err != nil {
		return MiseMergeResult{}, err
	}
	if len(tools) > 0 {
		root["tools"] = tools
	}
	tasks, err := mergeMiseTasks(shared.Tasks, selected.Tasks, resolver)
	if err != nil {
		return MiseMergeResult{}, err
	}
	if len(tasks) > 0 {
		root["tasks"] = tasks
	}
	if len(environment) > 0 {
		root["env"] = environment
	}

	config, err := extractMiseRoot("merged", root)
	if err != nil {
		return MiseMergeResult{}, err
	}
	data, err := encodeMiseConfig(config)
	if err != nil {
		return MiseMergeResult{}, err
	}
	changes := make([]ToolChange, 0)
	for name, selectedTool := range selected.Tools {
		mergedTool, ok := config.Tools[name]
		if !ok || anyEqual(selectedTool.Raw, mergedTool.Raw) {
			continue
		}
		changes = append(changes, ToolChange{Name: name, From: cloneAny(selectedTool.Raw), To: cloneAny(mergedTool.Raw)})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Name < changes[j].Name })
	return MiseMergeResult{Config: config, Bytes: data, ToolChanges: changes}, nil
}

func mergeMiseTools(shared, selected map[string]MiseTool, resolver MergeResolver) (map[string]any, error) {
	output := make(map[string]any)
	for _, name := range sortedUnionTypedKeys(shared, selected) {
		left, leftOK := shared[name]
		right, rightOK := selected[name]
		switch {
		case !leftOK:
			output[name] = cloneAny(right.Raw)
		case !rightOK:
			output[name] = cloneAny(left.Raw)
		case anyEqual(left.Raw, right.Raw):
			output[name] = cloneAny(right.Raw)
		case left.StringSelector && right.StringSelector:
			leftVersion, leftErr := semver.NewVersion(left.Selector)
			rightVersion, rightErr := semver.NewVersion(right.Selector)
			if leftErr == nil && rightErr == nil {
				if leftVersion.Major() != rightVersion.Major() {
					return nil, fmt.Errorf("mise tool %s has incompatible major versions %s and %s", name, left.Selector, right.Selector)
				}
				if leftVersion.GreaterThan(rightVersion) {
					output[name] = left.Selector
				} else {
					output[name] = right.Selector
				}
				continue
			}
			fallthrough
		default:
			value, err := resolveMiseScalar("tools."+name, left.Raw, right.Raw, false, resolver)
			if err != nil {
				return nil, err
			}
			output[name] = value
		}
	}
	return output, nil
}

func mergeMiseTasks(shared, selected map[string]MiseTask, resolver MergeResolver) (map[string]any, error) {
	output := make(map[string]any)
	for _, name := range sortedUnionTypedKeys(shared, selected) {
		left, leftOK := shared[name]
		right, rightOK := selected[name]
		switch {
		case !leftOK:
			output[name] = cloneMap(right.Raw)
		case !rightOK:
			output[name] = cloneMap(left.Raw)
		default:
			value, _, err := mergeMiseValue("tasks."+name, left.Raw, true, right.Raw, true, resolver)
			if err != nil {
				return nil, err
			}
			output[name] = value
		}
	}
	return output, nil
}

func mergeMiseEnvironment(shared, selected MiseConfig, selectedIdentity string, resolver MergeResolver) (map[string]any, map[string]any, error) {
	selectedRoot := cloneMap(selected.Root)
	selectedEnvironment := cloneMap(selected.Env)
	names := sortedMapKeys(selectedEnvironment)
	decisions := make(map[string]MergeDecision, len(names))

	// Resolve and apply every rename before building the merged environment so
	// references in values visited earlier receive later renames as well.
	for _, name := range names {
		selectedValue := selectedEnvironment[name]
		sharedValue, conflict := shared.Env[name]
		if !conflict || anyEqual(sharedValue, selectedValue) {
			decisions[name] = MergeDecision{Choice: MergeChoiceUseSelected}
			continue
		}
		decision, err := resolveConflict(resolver, MergeConflict{
			Path: "mise.toml", Key: name, Kind: "environment", Shared: fmt.Sprint(sharedValue), Selected: fmt.Sprint(selectedValue), AllowRename: true,
		})
		if err != nil {
			return nil, nil, err
		}
		switch decision.Choice {
		case MergeChoiceKeepShared, MergeChoiceUseSelected:
			decisions[name] = decision
		case MergeChoiceRenameSelected:
			newName := strings.TrimSpace(decision.Rename)
			if !validShellIdentifier(newName) {
				return nil, nil, fmt.Errorf("invalid environment rename %q for %s", newName, name)
			}
			if _, exists := shared.Env[newName]; exists {
				return nil, nil, fmt.Errorf("environment rename %q collides with shared key", newName)
			}
			if _, exists := selectedEnvironment[newName]; exists {
				return nil, nil, fmt.Errorf("environment rename %q collides with selected key", newName)
			}
			selectedRoot = rewriteEnvironmentReferences(selectedRoot, name, newName).(map[string]any)
			selectedEnvironment = selectedRoot["env"].(map[string]any)
			selectedValue = selectedEnvironment[name]
			delete(selectedEnvironment, name)
			selectedEnvironment[newName] = selectedValue
			selectedRoot["env"] = selectedEnvironment
			decision.Rename = newName
			decisions[name] = decision
		case MergeChoiceAbort:
			return nil, nil, fmt.Errorf("merge aborted for environment %s", name)
		default:
			return nil, nil, fmt.Errorf("invalid environment merge choice %q for %s", decision.Choice, name)
		}
	}

	output := cloneMap(shared.Env)
	for _, name := range names {
		decision := decisions[name]
		switch decision.Choice {
		case MergeChoiceKeepShared:
		case MergeChoiceUseSelected:
			output[name] = cloneAny(selectedEnvironment[name])
		case MergeChoiceRenameSelected:
			output[decision.Rename] = cloneAny(selectedEnvironment[decision.Rename])
		}
	}
	return selectedRoot, output, nil
}

func mergeMiseValue(path string, left any, leftOK bool, right any, rightOK bool, resolver MergeResolver) (any, bool, error) {
	if !leftOK {
		return cloneAny(right), rightOK, nil
	}
	if !rightOK {
		return cloneAny(left), true, nil
	}
	if anyEqual(left, right) {
		return cloneAny(right), true, nil
	}
	leftMap, leftMapOK := left.(map[string]any)
	rightMap, rightMapOK := right.(map[string]any)
	if leftMapOK && rightMapOK {
		output := make(map[string]any)
		for _, key := range sortedUnionKeys(leftMap, rightMap) {
			value, ok, err := mergeMiseValue(path+"."+key, leftMap[key], hasKey(leftMap, key), rightMap[key], hasKey(rightMap, key), resolver)
			if err != nil {
				return nil, false, err
			}
			if ok {
				output[key] = value
			}
		}
		return output, true, nil
	}
	if pathHasTaskRun(path) {
		leftCommands, leftOK := commandSequence(left)
		rightCommands, rightOK := commandSequence(right)
		if leftOK && rightOK {
			commands := append(leftCommands, rightCommands...)
			values := make([]any, len(commands))
			for index, command := range commands {
				values[index] = command
			}
			return values, true, nil
		}
	}
	leftList, leftListOK := anyList(left)
	rightList, rightListOK := anyList(right)
	if leftListOK && rightListOK {
		output := make([]any, 0, len(leftList)+len(rightList))
		for _, item := range append(leftList, rightList...) {
			found := false
			for _, existing := range output {
				if anyEqual(existing, item) {
					found = true
					break
				}
			}
			if !found {
				output = append(output, cloneAny(item))
			}
		}
		return output, true, nil
	}
	value, err := resolveMiseScalar(path, left, right, false, resolver)
	return value, true, err
}

func resolveMiseScalar(path string, shared, selected any, allowRename bool, resolver MergeResolver) (any, error) {
	decision, err := resolveConflict(resolver, MergeConflict{Path: "mise.toml", Key: path, Kind: "mise-value", Shared: fmt.Sprint(shared), Selected: fmt.Sprint(selected), AllowRename: allowRename})
	if err != nil {
		return nil, err
	}
	switch decision.Choice {
	case MergeChoiceKeepShared:
		return cloneAny(shared), nil
	case MergeChoiceUseSelected:
		return cloneAny(selected), nil
	case MergeChoiceAbort:
		return nil, fmt.Errorf("merge aborted for mise value %s", path)
	default:
		return nil, fmt.Errorf("invalid mise merge choice %q for %s", decision.Choice, path)
	}
}

func rewriteEnvironmentReferences(value any, oldName, newName string) any {
	switch typed := value.(type) {
	case string:
		text := strings.ReplaceAll(typed, "${"+oldName+"}", "${"+newName+"}")
		dollar := regexp.MustCompile(`\$` + regexp.QuoteMeta(oldName) + `([^A-Za-z0-9_]|$)`)
		text = dollar.ReplaceAllString(text, "$$"+newName+"$1")
		dot := regexp.MustCompile(`(^|[^A-Za-z0-9_])env\.` + regexp.QuoteMeta(oldName) + `([^A-Za-z0-9_]|$)`)
		return dot.ReplaceAllString(text, "${1}env."+newName+"$2")
	case map[string]any:
		output := make(map[string]any, len(typed))
		for key, item := range typed {
			output[key] = rewriteEnvironmentReferences(item, oldName, newName)
		}
		return output
	case []any:
		output := make([]any, len(typed))
		for index, item := range typed {
			output[index] = rewriteEnvironmentReferences(item, oldName, newName)
		}
		return output
	default:
		return cloneAny(typed)
	}
}

func validShellIdentifier(value string) bool {
	matched, _ := regexp.MatchString(`^[A-Za-z_][A-Za-z0-9_]*$`, value)
	return matched
}

func commandSequence(value any) ([]string, bool) {
	switch typed := value.(type) {
	case string:
		return []string{typed}, true
	case []any:
		commands := make([]string, len(typed))
		for index, item := range typed {
			command, ok := item.(string)
			if !ok {
				return nil, false
			}
			commands[index] = command
		}
		return commands, true
	case []string:
		return append([]string(nil), typed...), true
	default:
		return nil, false
	}
}

func anyList(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []string:
		output := make([]any, len(typed))
		for index, item := range typed {
			output[index] = item
		}
		return output, true
	default:
		return nil, false
	}
}

func pathHasTaskRun(path string) bool {
	parts := strings.Split(path, ".")
	return len(parts) >= 3 && parts[0] == "tasks" && parts[len(parts)-1] == "run"
}

func sortedUnionKeys(left, right map[string]any) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		seen[key] = struct{}{}
	}
	for key := range right {
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedUnionTypedKeys[T any](left, right map[string]T) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		seen[key] = struct{}{}
	}
	for key := range right {
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func hasKey(values map[string]any, key string) bool {
	_, ok := values[key]
	return ok
}
