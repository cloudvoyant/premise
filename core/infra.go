package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// InfraOptions selects an explicit Pulumi project, stack, and local backend.
type InfraOptions struct {
	Project     string
	Environment string
	All         bool
	Backend     string
	GCPProject  string
	Location    string
	Yes         bool
	Branch      string
	Shared      bool
}

// Public API -----------------------------------------------------------------

// RunInfra runs an operation against selected projects, with shared first.
// Every command specifies its stack; no operation relies on a selected stack.
func RunInfra(ctx context.Context, root, operation string, options InfraOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if operation == "plan" {
		operation = "preview"
	}
	if operation != "init" && operation != "preview" && operation != "apply" && operation != "output" && operation != "destroy" {
		return fmt.Errorf("unsupported infrastructure operation %q", operation)
	}
	stackName, err := InfraStack(options)
	if err != nil {
		return err
	}
	if operation == "destroy" && options.Environment != "preview" && options.Yes {
		return errors.New("destroy requires manual Pulumi confirmation outside preview; --yes is not allowed")
	}
	projects, err := selectInfraProjects(root, options)
	if err != nil {
		return err
	}
	if operation == "destroy" {
		if !options.Shared {
			projects = slices.DeleteFunc(projects, isSharedInfraProject)
		}
		if len(projects) == 0 {
			return errors.New("no environment infrastructure to destroy; shared requires --shared and manual confirmation")
		}
		slices.Reverse(projects)
	}
	if operation == "init" && (options.GCPProject == "" || options.Location == "") {
		return errors.New("infra init requires --gcp-project and --location")
	}
	backend, err := localPulumiBackend(root, options.Backend)
	if err != nil {
		return err
	}
	for _, project := range projects {
		directory, err := TemplateDirectory(root, project.Path)
		if err != nil {
			return err
		}
		stack := stackName
		if isSharedInfraProject(project) {
			stack = "shared"
		}
		if err := runInfraTool(ctx, directory, stdin, stdout, stderr, "pulumi", "login", backend); err != nil {
			return err
		}
		var arguments []string
		switch operation {
		case "init":
			if err := runInfraTool(ctx, directory, stdin, stdout, stderr, "pulumi", "stack", "select", stack, "--create", "--non-interactive"); err != nil {
				return err
			}
			for _, setting := range [][2]string{{"gcp:project", options.GCPProject}, {"location", options.Location}} {
				if err := runInfraTool(ctx, directory, stdin, stdout, stderr, "pulumi", "config", "set", setting[0], setting[1], "--stack", stack, "--non-interactive"); err != nil {
					return err
				}
			}
			continue
		case "preview":
			arguments = []string{"preview", "--stack", stack, "--non-interactive"}
		case "apply":
			arguments = []string{"up", "--stack", stack}
			if options.Yes {
				arguments = append(arguments, "--yes", "--non-interactive")
			}
		case "destroy":
			arguments = []string{"destroy", "--stack", stack}
			if options.Environment == "preview" {
				arguments = append(arguments, "--yes", "--non-interactive", "--remove")
			}
		case "output":
			arguments = []string{"stack", "output", "--stack", stack, "--json", "--non-interactive"}
		}
		fmt.Fprintf(stderr, "Infrastructure %s: %s/%s\n", operation, project.Name, stack)
		if err := runInfraTool(ctx, directory, stdin, stdout, stderr, "pulumi", arguments...); err != nil {
			return err
		}
	}
	return nil
}

// Private helpers ------------------------------------------------------------

func selectInfraProjects(root string, options InfraOptions) ([]Project, error) {
	if _, err := InfraStack(options); err != nil {
		return nil, err
	}
	projects, err := infrastructureProjects(root, options.Project, options.All, false)
	if err != nil {
		return nil, err
	}
	if options.Shared {
		projects = slices.DeleteFunc(projects, func(project Project) bool { return !isSharedInfraProject(project) })
	}
	if len(projects) == 0 {
		return nil, errors.New("no matching infrastructure project; scaffold one with pm g")
	}
	return projects, nil
}

func infrastructureProjects(root, name string, all, includeComponents bool) ([]Project, error) {
	if all == (name != "") {
		return nil, errors.New("select one infrastructure project or all projects")
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return nil, err
	}
	if manifest.Workspace.Providers.Infra != "pulumi" {
		return nil, errors.New("workspace infrastructure provider must be pulumi")
	}
	var projects []Project
	for _, project := range manifest.Workspace.Projects {
		if (isInfraProjectPath(project.Path) || (includeComponents && isInfraComponentPath(project.Path))) && (all || project.Name == name) {
			projects = append(projects, project)
		}
	}
	sort.SliceStable(projects, func(i, j int) bool {
		if isSharedInfraProject(projects[i]) != isSharedInfraProject(projects[j]) {
			return isSharedInfraProject(projects[i])
		}
		return projects[i].Name < projects[j].Name
	})
	return projects, nil
}

func localPulumiBackend(root, backend string) (string, error) {
	if backend == "" {
		absoluteRoot, err := filepath.Abs(root)
		if err != nil {
			return "", err
		}
		backend = (&url.URL{Scheme: "file", Path: filepath.Join(absoluteRoot, ".pulumi-state")}).String()
	}
	if !strings.HasPrefix(backend, "file:///") {
		return "", errors.New("this PoC requires an absolute file:// backend; backend provisioning is a separate experiment")
	}
	location, err := url.Parse(backend)
	if err != nil || location.Host != "" || location.RawQuery != "" || location.Fragment != "" || !filepath.IsAbs(location.Path) {
		return "", errors.New("local backend must be a plain absolute file URL")
	}
	if err := os.MkdirAll(location.Path, 0o700); err != nil {
		return "", fmt.Errorf("create local backend directory: %w", err)
	}
	return location.String(), nil
}

func runInfraTool(ctx context.Context, directory string, stdin io.Reader, stdout, stderr io.Writer, name string, arguments ...string) error {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	command.Env = os.Environ()
	if name == "pulumi" && len(arguments) > 0 && arguments[0] == "destroy" && !slices.Contains(arguments, "--yes") {
		command.Env = slices.DeleteFunc(command.Env, func(value string) bool { return strings.HasPrefix(value, "PULUMI_SKIP_CONFIRMATIONS=") })
		command.Env = append(command.Env, "PULUMI_SKIP_CONFIRMATIONS=false")
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(arguments, " "), err)
	}
	return nil
}

func isInfraProjectPath(path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	return path == "infra/environment" || strings.HasPrefix(path, "infra/projects/")
}

func isInfraComponentPath(path string) bool {
	return strings.HasPrefix(filepath.ToSlash(filepath.Clean(path)), "infra/components/")
}

func isSharedInfraProject(project Project) bool {
	if project.Infra != nil {
		return project.Infra.Shared
	}
	return project.Name == "shared"
}
