package core

var commonTasks = []string{
	"install",
	"build",
	"clean",
	"test",
	"lint",
	"lint:fix",
	"format",
	"format:check",
	"env-pull",
	"publish:rc",
	"publish",
}

var appTasks = []string{
	"run",
	"dev",
	"deploy",
	"e2e",
}

func ContractTasks(kind string) ([]string, error) {
	if _, err := KindDirectory(kind); err != nil {
		return nil, err
	}
	tasks := append([]string{}, commonTasks...)
	if kind == "app" {
		tasks = append(tasks, appTasks...)
	}
	return tasks, nil
}
