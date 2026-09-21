package core

// Mise extension responsibilities:
//   - provide the only process boundary to the Mise executable;
//   - apply workspace ceiling and environment isolation consistently;
//   - inspect and execute tasks for install, template, CI, and release modules.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/pelletier/go-toml/v2"
)

// Types -----------------------------------------------------------------------

type miseRunner struct {
	Stdout            io.Writer
	Stderr            io.Writer
	Ceiling           string
	RemoveEnvironment []string
}

type MiseTool struct {
	Raw            any
	Selector       string
	StringSelector bool
}

type MiseTask struct {
	Raw       map[string]any
	Run       []string
	Shorthand bool
}

type MiseConfig struct {
	Root  map[string]any
	Tools map[string]MiseTool
	Tasks map[string]MiseTask
	Env   map[string]any
}

type ToolChange struct {
	Name string
	From any
	To   any
}

type MiseMergeResult struct {
	Config      MiseConfig
	Bytes       []byte
	ToolChanges []ToolChange
	Notices     []string
}

// API -------------------------------------------------------------------------

// ExtractMiseConfig parses and normalizes a Mise configuration.
func ExtractMiseConfig(label string, data []byte) (MiseConfig, error) {
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return MiseConfig{}, fmt.Errorf("decode %s mise.toml: %w", label, err)
	}
	return extractMiseRoot(label, root)
}

// MergeMiseConfigs combines typed shared and selected Mise configurations.
func MergeMiseConfigs(shared, selected MiseConfig, templateKind, registryIdentity string, resolver MergeConflictResolver) (MiseMergeResult, error) {
	selectedRoot, environment, err := mergeMiseEnvironment(shared, selected, registryIdentity, resolver)
	if err != nil {
		return MiseMergeResult{}, err
	}
	selected, err = extractMiseRoot(registryIdentity, selectedRoot)
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
	tasks, notices, err := mergeMiseTasks(shared.Tasks, selected.Tasks, templateKind, registryIdentity)
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
		changes = append(changes, ToolChange{Name: name, From: cloneMiseValue(selectedTool.Raw), To: cloneMiseValue(mergedTool.Raw)})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Name < changes[j].Name })
	sort.Strings(notices)
	return MiseMergeResult{Config: config, Bytes: data, ToolChanges: changes, Notices: notices}, nil
}

// Utils -----------------------------------------------------------------------

var publicationCredentialEnvironment = []string{
	"GITHUB_TOKEN",
	"GH_TOKEN",
	"CARGO_REGISTRY_TOKEN",
	"CARGO_TOKEN",
	"CRATES_TOKEN",
}

// run executes one Mise command.
func (runner miseRunner) run(ctx context.Context, directory string, additions []string, arguments ...string) error {
	command := exec.CommandContext(ctx, "mise", arguments...)
	command.Dir = directory
	command.Env = runner.environment(additions)
	command.Stdout = runner.Stdout
	command.Stderr = runner.Stderr
	return command.Run()
}

// taskExists reports whether a Mise task selector resolves in directory.
func (runner miseRunner) taskExists(ctx context.Context, directory, task string) (bool, error) {
	command := exec.CommandContext(ctx, "mise", "task", "info", task, "--json")
	command.Dir = directory
	command.Env = runner.environment(nil)
	var commandError bytes.Buffer
	command.Stdout = io.Discard
	command.Stderr = &commandError
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(commandError.String())
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 && strings.Contains(message, "Task not found") {
			return false, nil
		}
		if message != "" {
			return false, errors.New(message)
		}
		return false, err
	}
	return true, nil
}

// toolEnvironment resolves an environment containing explicitly selected Mise tools.
// Mise itself runs with the runner's sanitized environment; the returned environment
// can then be used to launch the intended tool without exposing credentials to Mise.
func (runner miseRunner) toolEnvironment(ctx context.Context, directory string, tools ...string) ([]string, error) {
	arguments := append([]string{"exec"}, tools...)
	arguments = append(arguments, "--", "env", "-0")
	command := exec.CommandContext(ctx, "mise", arguments...)
	command.Dir = directory
	command.Env = runner.environment(nil)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = runner.Stderr
	if err := command.Run(); err != nil {
		return nil, err
	}
	entries := strings.Split(strings.TrimSuffix(output.String(), "\x00"), "\x00")
	if len(entries) == 1 && entries[0] == "" {
		return nil, errors.New("mise returned an empty tool environment")
	}
	return entries, nil
}

// environment returns the process environment used for Mise-aware commands.
func (runner miseRunner) environment(additions []string) []string {
	removed := append([]string(nil), publicationCredentialEnvironment...)
	removed = append(removed, runner.RemoveEnvironment...)
	if runner.Ceiling != "" {
		removed = append(removed, "MISE_CEILING_PATHS")
	}
	for _, addition := range additions {
		if name, _, ok := strings.Cut(addition, "="); ok {
			removed = append(removed, name)
		}
	}
	environment := withoutEnvironment(os.Environ(), removed...)
	if runner.Ceiling != "" {
		environment = append(environment, "MISE_CEILING_PATHS="+runner.Ceiling)
	}
	return append(environment, additions...)
}

// withoutEnvironment removes named variables while preserving every other entry.
func withoutEnvironment(environment []string, names ...string) []string {
	blocked := make(map[string]struct{}, len(names))
	for _, name := range names {
		blocked[name] = struct{}{}
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, ok := strings.Cut(entry, "=")
		if _, remove := blocked[name]; ok && remove {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func extractMiseRoot(label string, input map[string]any) (MiseConfig, error) {
	root := cloneMiseValue(input).(map[string]any)
	config := MiseConfig{
		Root:  root,
		Tools: make(map[string]MiseTool),
		Tasks: make(map[string]MiseTask),
		Env:   make(map[string]any),
	}
	if raw, ok := root["tools"]; ok {
		tools, ok := raw.(map[string]any)
		if !ok {
			return MiseConfig{}, fmt.Errorf("%s mise.toml section tools must be a table", label)
		}
		for name, value := range tools {
			selector, stringSelector := value.(string)
			config.Tools[name] = MiseTool{Raw: cloneMiseValue(value), Selector: selector, StringSelector: stringSelector}
		}
	}
	if raw, ok := root["tasks"]; ok {
		tasks, ok := raw.(map[string]any)
		if !ok {
			return MiseConfig{}, fmt.Errorf("%s mise.toml section tasks must be a table", label)
		}
		normalized := make(map[string]any, len(tasks))
		for name, value := range tasks {
			task, err := extractMiseTask(label, name, value)
			if err != nil {
				return MiseConfig{}, err
			}
			config.Tasks[name] = task
			normalized[name] = cloneMiseValue(task.Raw).(map[string]any)
		}
		root["tasks"] = normalized
	}
	if raw, ok := root["env"]; ok {
		environment, ok := raw.(map[string]any)
		if !ok {
			return MiseConfig{}, fmt.Errorf("%s mise.toml section env must be a table", label)
		}
		config.Env = cloneMiseValue(environment).(map[string]any)
		root["env"] = cloneMiseValue(environment).(map[string]any)
	}
	return config, nil
}

func extractMiseTask(label, name string, value any) (MiseTask, error) {
	if shorthand, ok := value.(string); ok {
		return MiseTask{Raw: map[string]any{"run": shorthand}, Run: []string{shorthand}, Shorthand: true}, nil
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return MiseTask{}, fmt.Errorf("%s mise.toml task %s must be a command string or table", label, name)
	}
	task := MiseTask{Raw: cloneMiseValue(raw).(map[string]any)}
	run, ok := raw["run"]
	if !ok {
		return task, nil
	}
	switch typed := run.(type) {
	case string:
		task.Run = []string{typed}
	case []any:
		task.Run = make([]string, len(typed))
		for index, command := range typed {
			text, ok := command.(string)
			if !ok {
				return MiseTask{}, fmt.Errorf("%s mise.toml task %s run entry %d must be a string", label, name, index)
			}
			task.Run[index] = text
		}
	case []string:
		task.Run = append([]string(nil), typed...)
	default:
		return MiseTask{}, fmt.Errorf("%s mise.toml task %s run must be a string or string list", label, name)
	}
	return task, nil
}

func encodeMiseConfig(config MiseConfig) ([]byte, error) {
	data, err := toml.Marshal(config.Root)
	if err != nil {
		return nil, fmt.Errorf("encode merged mise.toml: %w", err)
	}
	return data, nil
}

// updateMiseTool updates one tool through the typed Mise representation and
// rewrites the config at path using canonical TOML encoding.
func updateMiseTool(path, name string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read mise config %s: %w", path, err)
	}
	config, err := ExtractMiseConfig(path, data)
	if err != nil {
		return err
	}
	tools, ok := config.Root["tools"].(map[string]any)
	if !ok {
		return fmt.Errorf("mise config %s tools section must be a table", path)
	}
	if _, ok := tools[name]; !ok {
		return fmt.Errorf("mise config %s does not contain tool %s", path, name)
	}
	tools[name] = cloneMiseValue(value)
	config, err = extractMiseRoot(path, config.Root)
	if err != nil {
		return err
	}
	encoded, err := encodeMiseConfig(config)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect mise config %s: %w", path, err)
	}
	if err := os.WriteFile(path, encoded, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write mise config %s: %w", path, err)
	}
	return nil
}

// cloneMiseValue recursively copies the TOML value shapes used by Mise.
func cloneMiseValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := maps.Clone(typed)
		for key, item := range cloned {
			cloned[key] = cloneMiseValue(item)
		}
		return cloned
	case []any:
		cloned := slices.Clone(typed)
		for index, item := range cloned {
			cloned[index] = cloneMiseValue(item)
		}
		return cloned
	case []string:
		return slices.Clone(typed)
	default:
		return typed
	}
}

func anyEqual(left, right any) bool {
	return reflect.DeepEqual(left, right)
}

func composeMise(shared, selected []byte, templateKind, registryIdentity string, resolver MergeConflictResolver) (MiseMergeResult, error) {
	sharedConfig, err := ExtractMiseConfig("shared", shared)
	if err != nil {
		return MiseMergeResult{}, err
	}
	selectedConfig, err := ExtractMiseConfig(registryIdentity, selected)
	if err != nil {
		return MiseMergeResult{}, err
	}
	return MergeMiseConfigs(sharedConfig, selectedConfig, templateKind, registryIdentity, resolver)
}

func mergeMiseTools(shared, selected map[string]MiseTool, resolver MergeConflictResolver) (map[string]any, error) {
	output := make(map[string]any)
	for _, name := range sortedUnionTypedKeys(shared, selected) {
		left, leftOK := shared[name]
		right, rightOK := selected[name]
		switch {
		case !leftOK:
			output[name] = cloneMiseValue(right.Raw)
		case !rightOK:
			output[name] = cloneMiseValue(left.Raw)
		case anyEqual(left.Raw, right.Raw):
			output[name] = cloneMiseValue(right.Raw)
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

func mergeMiseTasks(shared, selected map[string]MiseTask, templateKind, registryIdentity string) (map[string]any, []string, error) {
	contractTasks, err := ContractTasks(templateKind)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve %s contract tasks: %w", templateKind, err)
	}
	contracts := make(map[string]struct{}, len(contractTasks))
	for _, name := range contractTasks {
		contracts[name] = struct{}{}
	}
	output := make(map[string]any)
	var notices []string
	prefix := miseTaskPrefix(registryIdentity)
	for _, name := range sortedUnionTypedKeys(shared, selected) {
		left, leftOK := shared[name]
		right, rightOK := selected[name]
		switch {
		case !leftOK:
			output[name] = cloneMiseValue(right.Raw).(map[string]any)
		case !rightOK:
			output[name] = cloneMiseValue(left.Raw).(map[string]any)
		case isContractTask(contracts, name):
			// Contract metadata belongs to the selected template. Only its run
			// body is composed; contract tasks are intentionally argument-free.
			merged := cloneMiseValue(right.Raw).(map[string]any)
			commands := make([]string, 0, len(left.Run)+len(right.Run))
			commands = append(commands, left.Run...)
			commands = append(commands, right.Run...)
			merged["run"] = commands
			output[name] = merged
		default:
			namespaced := prefix + ":" + name
			if _, exists := shared[namespaced]; exists {
				return nil, nil, fmt.Errorf("cannot namespace Mise task %q as %q: task name already exists", name, namespaced)
			}
			if _, exists := selected[namespaced]; exists {
				return nil, nil, fmt.Errorf("cannot namespace Mise task %q as %q: task name already exists", name, namespaced)
			}
			output[name] = cloneMiseValue(left.Raw).(map[string]any)
			output[namespaced] = cloneMiseValue(right.Raw).(map[string]any)
			notices = append(notices, fmt.Sprintf("Mise task %q was namespaced as %q to preserve the shared task", name, namespaced))
		}
	}
	return output, notices, nil
}

func isContractTask(contractTasks map[string]struct{}, name string) bool {
	_, ok := contractTasks[name]
	return ok
}

func miseTaskPrefix(identity string) string {
	identity = strings.TrimSpace(identity)
	if separator := strings.LastIndex(identity, ":"); separator >= 0 && !strings.ContainsAny(identity[separator+1:], "/\\\\") {
		identity = identity[:separator]
	}
	identity = strings.TrimSuffix(filepath.Base(filepath.Clean(filepath.FromSlash(identity))), ".git")
	if identity == "." || identity == string(filepath.Separator) || identity == "" {
		identity = "registry"
	}
	identity = regexp.MustCompile(`[^A-Za-z0-9_-]+`).ReplaceAllString(identity, "-")
	identity = strings.Trim(identity, "-_")
	if identity == "" {
		return "registry"
	}
	return identity
}

func mergeMiseEnvironment(shared, selected MiseConfig, selectedIdentity string, resolver MergeConflictResolver) (map[string]any, map[string]any, error) {
	selectedRoot := cloneMiseValue(selected.Root).(map[string]any)
	selectedEnvironment := cloneMiseValue(selected.Env).(map[string]any)
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

	output := cloneMiseValue(shared.Env).(map[string]any)
	for _, name := range names {
		decision := decisions[name]
		switch decision.Choice {
		case MergeChoiceKeepShared:
		case MergeChoiceUseSelected:
			output[name] = cloneMiseValue(selectedEnvironment[name])
		case MergeChoiceRenameSelected:
			output[decision.Rename] = cloneMiseValue(selectedEnvironment[decision.Rename])
		}
	}
	return selectedRoot, output, nil
}

func mergeMiseValue(path string, left any, leftOK bool, right any, rightOK bool, resolver MergeConflictResolver) (any, bool, error) {
	if !leftOK {
		return cloneMiseValue(right), rightOK, nil
	}
	if !rightOK {
		return cloneMiseValue(left), true, nil
	}
	if anyEqual(left, right) {
		return cloneMiseValue(right), true, nil
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
				output = append(output, cloneMiseValue(item))
			}
		}
		return output, true, nil
	}
	value, err := resolveMiseScalar(path, left, right, false, resolver)
	return value, true, err
}

func resolveMiseScalar(path string, shared, selected any, allowRename bool, resolver MergeConflictResolver) (any, error) {
	decision, err := resolveConflict(resolver, MergeConflict{Path: "mise.toml", Key: path, Kind: "mise-value", Shared: fmt.Sprint(shared), Selected: fmt.Sprint(selected), AllowRename: allowRename})
	if err != nil {
		return nil, err
	}
	switch decision.Choice {
	case MergeChoiceKeepShared:
		return cloneMiseValue(shared), nil
	case MergeChoiceUseSelected:
		return cloneMiseValue(selected), nil
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
		return cloneMiseValue(typed)
	}
}

func validShellIdentifier(value string) bool {
	matched, _ := regexp.MatchString(`^[A-Za-z_][A-Za-z0-9_]*$`, value)
	return matched
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
