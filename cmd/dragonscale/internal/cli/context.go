package cli

import (
	"io"
	"os"

	"github.com/ZanzyTHEbar/dragonscale/pkg/dragonscale/sdk"
)

// AppContext carries shared dependencies for CLI command construction.
type AppContext struct {
	Service *sdk.Service

	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	Version   string
	GitCommit string
	BuildTime string
	GoVersion string
}

// NewAppContext creates a CLI context with resilient defaults.
func NewAppContext(service *sdk.Service, version, gitCommit, buildTime, goVersion string) *AppContext {
	if service == nil {
		service = sdk.NewService(sdk.WithVersion(version, gitCommit, buildTime, goVersion))
	} else {
		if version != "" {
			service.Version = version
		}
		if gitCommit != "" {
			service.GitCommit = gitCommit
		}
		if buildTime != "" {
			service.BuildTime = buildTime
		}
		if goVersion != "" {
			service.GoVersion = goVersion
		}
	}

	return &AppContext{
		Service:   service,
		In:        os.Stdin,
		Out:       os.Stdout,
		ErrOut:    os.Stderr,
		Version:   service.Version,
		GitCommit: service.GitCommit,
		BuildTime: service.BuildTime,
		GoVersion: service.GoVersion,
	}
}

// WithIO returns a copy with explicit streams.
func (c *AppContext) WithIO(in io.Reader, out io.Writer, errOut io.Writer) *AppContext {
	if c == nil {
		return nil
	}
	copy := *c
	if in != nil {
		copy.In = in
	}
	if out != nil {
		copy.Out = out
	}
	if errOut != nil {
		copy.ErrOut = errOut
	}
	return &copy
}

func (c *AppContext) stdout() io.Writer {
	if c == nil || c.Out == nil {
		return os.Stdout
	}
	return c.Out
}

func (c *AppContext) stderr() io.Writer {
	if c == nil || c.ErrOut == nil {
		return os.Stderr
	}
	return c.ErrOut
}
