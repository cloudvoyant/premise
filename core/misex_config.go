package core

import (
	"fmt"
	"reflect"

	"github.com/pelletier/go-toml/v2"
)

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
}

func ExtractMiseConfig(label string, data []byte) (MiseConfig, error) {
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return MiseConfig{}, fmt.Errorf("decode %s mise.toml: %w", label, err)
	}
	return extractMiseRoot(label, root)
}

func extractMiseRoot(label string, input map[string]any) (MiseConfig, error) {
	root := cloneMap(input)
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
			config.Tools[name] = MiseTool{Raw: cloneAny(value), Selector: selector, StringSelector: stringSelector}
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
			normalized[name] = cloneMap(task.Raw)
		}
		root["tasks"] = normalized
	}
	if raw, ok := root["env"]; ok {
		environment, ok := raw.(map[string]any)
		if !ok {
			return MiseConfig{}, fmt.Errorf("%s mise.toml section env must be a table", label)
		}
		config.Env = cloneMap(environment)
		root["env"] = cloneMap(environment)
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
	task := MiseTask{Raw: cloneMap(raw)}
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

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = cloneAny(value)
	}
	return output
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		output := make([]any, len(typed))
		for index, item := range typed {
			output[index] = cloneAny(item)
		}
		return output
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}

func anyEqual(left, right any) bool {
	return reflect.DeepEqual(left, right)
}
