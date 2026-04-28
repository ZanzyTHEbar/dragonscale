package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/format"
	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/runner"
)

type Task interface {
	Name() string
	Description() string
	Run(ctx context.Context, r runner.Runner, io *Context) (Result, error)
}

type Result struct {
	Task     string
	Command  string
	ExitCode int
	Stdout   string
	Stderr   string
	Err      string
}

type Context struct {
	Root         string
	Cwd          string
	Format       format.OutputMode
	Quiet        bool
	NoColor      bool
	Timeout      time.Duration
	ExtraEnv     map[string]string
	Argv         []string
	Stdout       io.Writer
	Stderr       io.Writer
	Debug        bool
	RunID        string
	DevContainer bool
}

type App struct {
	tasks  map[string]Task
	order  []string
	root   string
	runner runner.Runner
	clock  func() time.Time
}

func New(r runner.Runner, root string) *App {
	return &App{
		tasks:  make(map[string]Task),
		root:   root,
		runner: r,
		clock:  time.Now,
	}
}

func (a *App) Register(task Task) {
	name := task.Name()
	a.tasks[name] = task
	a.order = append(a.order, name)
}

func (a *App) Task(name string) (Task, bool) {
	task, ok := a.tasks[name]
	return task, ok
}

func (a *App) List() []Task {
	taskList := make([]Task, 0, len(a.tasks))
	seen := make(map[string]bool, len(a.tasks))
	for _, name := range a.order {
		task, ok := a.tasks[name]
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		taskList = append(taskList, task)
	}
	for name, task := range a.tasks {
		if seen[name] {
			continue
		}
		taskList = append(taskList, task)
	}
	sort.Slice(taskList, func(i, j int) bool {
		return taskList[i].Name() < taskList[j].Name()
	})
	return taskList
}

func (a *App) HasTask(name string) bool {
	_, ok := a.tasks[name]
	return ok
}

func (a *App) Run(ctx context.Context, name string, args []string, outCtx *Context) (format.TaskResult, error) {
	task, ok := a.tasks[name]
	if !ok {
		return format.TaskResult{}, fmt.Errorf("unknown task: %s", name)
	}

	ctxWithTimeout := ctx
	start := a.clock()
	if outCtx.Timeout > 0 {
		var cancel context.CancelFunc
		ctxWithTimeout, cancel = context.WithTimeout(ctx, outCtx.Timeout)
		defer cancel()
	}

	taskCtx := *outCtx
	taskCtx.Cwd = nonEmpty(taskCtx.Cwd, taskCtx.Root)
	taskCtx.Argv = args

	res, err := task.Run(ctxWithTimeout, a.runner, &taskCtx)
	finish := a.clock()
	success := err == nil && res.Err == ""
	tr := format.TaskResult{
		Task:       name,
		Command:    res.Command,
		ExitCode:   res.ExitCode,
		Stdout:     res.Stdout,
		Stderr:     res.Stderr,
		Error:      res.Err,
		NoColor:    outCtx.NoColor,
		StartedAt:  start,
		EndedAt:    finish,
		DurationMS: int64(finish.Sub(start) / time.Millisecond),
		Success:    success,
	}
	if err != nil {
		if strings.TrimSpace(res.Err) != "" {
			tr.Error = res.Err
		} else {
			tr.Error = err.Error()
		}
	}
	if tr.Error != "" && tr.ExitCode == 0 {
		tr.ExitCode = 1
	}
	return tr, err
}

func nonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (a *App) formatTaskHelp() []string {
	lines := make([]string, 0, len(a.tasks)+1)
	lines = append(lines, "available tasks:")
	for _, t := range a.List() {
		lines = append(lines, fmt.Sprintf("  %-20s  %s", t.Name(), t.Description()))
	}
	lines = append(lines, "\nenvironment:")
	lines = append(lines, "  DRAGONSCALE_EVAL_HOST_HOME")
	lines = append(lines, "  DRAGONSCALE_EVAL_BASE_CONFIG")
	lines = append(lines, "  DRAGONSCALE_EVAL_CONFIG")
	lines = append(lines, "  SKIP_DEVCONTAINER_WRAPPER")
	lines = append(lines, "  DEVCONTAINER_EXEC")
	lines = append(lines, "  DRAGONSCALE_EVAL_DEBUG")
	return lines
}

func (a *App) TaskHelp() []string {
	return a.formatTaskHelp()
}

func DefaultRoot() string {
	wd, err := os.Getwd()
	if err == nil {
		return wd
	}
	return ""
}

func EnsureRoot(root string) string {
	clean := filepath.Clean(root)
	if clean == "." || clean == "" {
		return DefaultRoot()
	}
	return clean
}
