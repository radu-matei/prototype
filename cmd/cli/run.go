package main

import (
	"os"
	"path/filepath"

	docker "github.com/docker/docker/client"
	drakecli "github.com/radu-matei/prototype/pkg/cli"
	drakedocker "github.com/radu-matei/prototype/pkg/orchestration/docker"
	"github.com/radu-matei/prototype/pkg/signals"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

func run(c *cli.Context) error {
	// This context will automatically be canceled on SIGINT or SIGTERM.
	ctx := signals.Context()
	configFile := c.GlobalString(flagFile)
	secretsFile := c.String(flagSecretsFile)
	debugOnly := c.Bool(flagDebug)
	concurrencyEnabled := c.Bool(flagConcurrently)
	absConfigFilePath, err := filepath.Abs(configFile)
	if err != nil {
		return err
	}
	sourcePath := filepath.Dir(absConfigFilePath)
	dockerClient, err := docker.NewClientWithOpts(docker.FromEnv)
	if err != nil {
		return errors.Wrap(err, "error building Docker client")
	}
	executor := drakecli.NewExecutor(
		dockerClient,
		drakedocker.NewOrchestrator(dockerClient),
	)
	executePipelines := c.Bool(flagPipeline)
	if executePipelines {
		if len(c.Args()) == 0 {
			return errors.New("no pipelines were specified for execution")
		}
		// TODO: Should pass the stream that we want output to go to-- stdout
		err = executor.ExecutePipelines(
			ctx,
			configFile,
			secretsFile,
			sourcePath,
			c.Args(),
			debugOnly,
			concurrencyEnabled,
		)
	} else {
		if len(c.Args()) == 0 {
			return errors.New("no targets were specified for execution")
		}
		// TODO: Should pass the stream that we want output to go to-- stdout
		err = executor.ExecuteTargets(
			ctx,
			configFile,
			secretsFile,
			sourcePath,
			c.Args(),
			debugOnly,
			concurrencyEnabled,
		)
	}
	select {
	case <-ctx.Done():
		os.Exit(1)
	default:
	}
	return err
}
