// Package sdk provides a reusable, IO-agnostic dragonscale service API.
package sdk

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"runtime"
)

const defaultLogo = "🐉"

// Service is the public entry point for CLI and other I/O adapters.
type Service struct {
	Version    string
	GitCommit  string
	BuildTime  string
	GoVersion  string
	Logo       string
	EmbeddedFS fs.FS
}

// CLIService contracts represent the public service API consumed by I/O adapters.
type CLIService interface {
	VersionService
	OnboardingService
	AgentService
	GatewayService
	AuthService
	CronService
	SkillsService
	SecretService
	DaemonService
	MemoryService
	StatusService
}

// NewService creates a configured service instance.
func NewService(opts ...Option) *Service {
	svc := &Service{
		Version: "dev",
		Logo:    defaultLogo,
	}

	for _, opt := range opts {
		opt(svc)
	}

	if svc.GoVersion == "" {
		svc.GoVersion = runtime.Version()
	}

	if svc.Logo == "" {
		svc.Logo = defaultLogo
	}

	return svc
}

// Option configures a Service.
type Option func(*Service)

// WithVersion sets version metadata.
func WithVersion(version, gitCommit, buildTime, goVersion string) Option {
	return func(s *Service) {
		s.Version = version
		s.GitCommit = gitCommit
		s.BuildTime = buildTime
		s.GoVersion = goVersion
	}
}

// WithLogo sets the CLI logo glyph.
func WithLogo(logo string) Option {
	return func(s *Service) {
		s.Logo = logo
	}
}

// WithEmbeddedFS sets the embedded template filesystem.
func WithEmbeddedFS(fsys fs.FS) Option {
	return func(s *Service) {
		s.EmbeddedFS = fsys
	}
}

// VersionString returns the version text with optional git commit suffix.
func (s *Service) VersionString() string {
	v := s.Version
	if v == "" {
		v = "dev"
	}
	if s.GitCommit != "" {
		v += fmt.Sprintf(" (git: %s)", s.GitCommit)
	}
	return v
}

// BuildInfo returns build metadata.
func (s *Service) BuildInfo() (build string, goVer string) {
	build = s.BuildTime
	goVer = s.GoVersion
	return
}

// OnboardingService defines onboarding operations.
type OnboardingService interface {
	Onboard(context.Context, io.Reader, io.Writer) error
}

// AgentService defines interactive and one-shot agent operations.
type AgentService interface {
	Agent(context.Context, io.Reader, io.Writer, AgentOptions) error
}

// GatewayService defines gateway startup and control operations.
type GatewayService interface {
	Gateway(context.Context, io.Writer, GatewayOptions) error
}

// VersionService defines version reporting operations.
type VersionService interface {
	PrintVersion(context.Context, io.Writer) error
}

// StatusService defines status reporting operations.
type StatusService interface {
	Status(context.Context, io.Writer) error
}

// AuthService defines auth lifecycle operations.
type AuthService interface {
	AuthLogin(context.Context, io.Reader, io.Writer, string, bool) error
	AuthLogout(context.Context, io.Writer, string) error
	AuthStatus(context.Context, io.Writer) error
}

// CronService defines cron job operations.
type CronService interface {
	CronList(context.Context, io.Writer) error
	CronAdd(context.Context, io.Writer, CronAddOptions) error
	CronRemove(context.Context, io.Writer, string) error
	CronEnable(context.Context, io.Writer, string, bool) error
}

// SkillsService defines skill lifecycle operations.
type SkillsService interface {
	SkillsList(context.Context, io.Writer) error
	SkillsListBuiltin(context.Context, io.Writer) error
	SkillsInstall(context.Context, io.Writer, string) error
	SkillsInstallBuiltin(context.Context, io.Writer, string) error
	SkillsRemove(context.Context, io.Writer, string) error
	SkillsSearch(context.Context, io.Writer) error
	SkillsShow(context.Context, io.Writer, string) error
}

// SecretService defines encrypted secret management operations.
type SecretService interface {
	SecretInit(context.Context, io.Writer) error
	SecretAdd(context.Context, io.Reader, io.Writer, string) error
	SecretList(context.Context, io.Writer) error
	SecretDelete(context.Context, io.Writer, string) error
}

// DaemonService defines daemon operations.
type DaemonService interface {
	DaemonStart(context.Context, io.Writer) error
	DaemonStop(context.Context, io.Writer) error
	DaemonStatus(context.Context, io.Writer) error
}

// MemoryService defines memory system operations.
type MemoryService interface {
	MemoryDBStatus(context.Context, io.Writer) error
}

var (
	_ VersionService    = (*Service)(nil)
	_ CLIService        = (*Service)(nil)
	_ OnboardingService = (*Service)(nil)
	_ AgentService      = (*Service)(nil)
	_ GatewayService    = (*Service)(nil)
	_ StatusService     = (*Service)(nil)
	_ AuthService       = (*Service)(nil)
	_ CronService       = (*Service)(nil)
	_ SkillsService     = (*Service)(nil)
	_ SecretService     = (*Service)(nil)
	_ DaemonService     = (*Service)(nil)
	_ MemoryService     = (*Service)(nil)
)
