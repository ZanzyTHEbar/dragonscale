// DragonScale - Ultra-lightweight personal AI agent
// Inspired by and based on picoclaw: https://github.com/sipeed/picoclaw
// License: MIT
//
// Copyright (c) 2026 DragonScale contributors

package main

import (
	"embed"
	"os"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli/commands"
	"github.com/ZanzyTHEbar/dragonscale/pkg/dragonscale/sdk"
)

//go:generate cp -r ../../workspace .
//go:embed workspace
var embeddedFiles embed.FS

var (
	version   = "dev"
	gitCommit string
	buildTime string
	goVersion string
)

func main() {
	ctx := cli.NewAppContext(
		sdk.NewService(
			sdk.WithVersion(version, gitCommit, buildTime, goVersion),
			sdk.WithEmbeddedFS(embeddedFiles),
		),
		version,
		gitCommit,
		buildTime,
		goVersion,
	)

	root := commands.BuildRoot(ctx)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
