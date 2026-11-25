package backends

import (
	"errors"
	"fmt"
	"slices"
)

// Private helpers ------------------------------------------------------------

type workspacePackage struct {
	name         string
	publishable  bool
	dependencies []string
}

func validateWorkspaceDependencies(ecosystem, unit string, packages []workspacePackage) error {
	publishable := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		publishable[pkg.name] = pkg.publishable
	}
	var failures []error
	for _, pkg := range packages {
		if !pkg.publishable {
			continue
		}
		deps := append([]string(nil), pkg.dependencies...)
		slices.Sort(deps)
		for _, dep := range deps {
			if isPublishable, isWorkspace := publishable[dep]; isWorkspace && !isPublishable {
				failures = append(failures, fmt.Errorf("publishable %s %s %s depends on local-only workspace %s %s", ecosystem, unit, pkg.name, unit, dep))
			}
		}
	}
	return errors.Join(failures...)
}

func topologicalOrder(nodes []string, deps map[string][]string) ([]string, error) {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(nodes))
	out := make([]string, 0, len(nodes))
	var visit func(string) error
	visit = func(node string) error {
		switch state[node] {
		case visiting:
			return fmt.Errorf("cyclic workspace dependency involving %s", node)
		case visited:
			return nil
		}
		state[node] = visiting
		for _, dep := range deps[node] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[node] = visited
		out = append(out, node)
		return nil
	}
	for _, node := range nodes {
		if err := visit(node); err != nil {
			return nil, err
		}
	}
	return out, nil
}
