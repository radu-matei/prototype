package cli

import (
	"context"
	"fmt"

	"github.com/lovethedrake/prototype/pkg/config"
	"github.com/lovethedrake/prototype/pkg/orchestration"
	"github.com/technosophos/moniker"
)

// Executor is the public interface for the CLI executor
type Executor interface {
	ExecuteTargets(
		ctx context.Context,
		configFile string,
		sourcePath string,
		targetNames []string,
		debugOnly bool,
		concurrencyEnabled bool,
	) error
	ExecutePipelines(
		ctx context.Context,
		configFile string,
		sourcePath string,
		pipelineNames []string,
		debugOnly bool,
		concurrencyEnabled bool,
	) error
}

type executor struct {
	namer        moniker.Namer
	orchestrator orchestration.Orchestrator
}

// NewExecutor returns an executor suitable for use with local development
func NewExecutor(orchestrator orchestration.Orchestrator) Executor {
	return &executor{
		namer:        moniker.New(),
		orchestrator: orchestrator,
	}
}

func (e *executor) ExecuteTargets(
	ctx context.Context,
	configFile string,
	sourcePath string,
	targetNames []string,
	debugOnly bool,
	concurrencyEnabled bool,
) error {
	config, err := config.NewConfigFromFile(configFile)
	if err != nil {
		return err
	}
	targets, err := config.GetTargets(targetNames)
	if err != nil {
		return err
	}
	if debugOnly {
		fmt.Printf("would execute targets: %s\n", targetNames)
		return nil
	}
	executionName := e.namer.NameSep("-")
	errCh := make(chan error)
	var runningTargets int
	for _, target := range targets {
		targetExecutionName := fmt.Sprintf("%s-%s", executionName, target.Name())
		runningTargets++
		go e.orchestrator.ExecuteTarget(
			ctx,
			targetExecutionName,
			sourcePath,
			target,
			errCh,
		)
		if !concurrencyEnabled {
			// If concurrency isn't enabled, wait for a potential error. If it's nil,
			// move on. If it's not, return the error.
			if err := <-errCh; err != nil {
				return err
			}
			runningTargets--
		}
	}
	// If concurrency isn't enabled and we haven't already encountered an error,
	// then we're not going to. We're done!
	if !concurrencyEnabled {
		return nil
	}
	// Wait for all the targets to finish.
	errs := []error{}
	for err := range errCh {
		if err != nil {
			errs = append(errs, err)
		}
		runningTargets--
		if runningTargets == 0 {
			break
		}
	}
	if len(errs) > 1 {
		return &multiError{errs: errs}
	}
	if len(errs) == 1 {
		return errs[0]
	}
	return nil
}

func (e *executor) ExecutePipelines(
	ctx context.Context,
	configFile string,
	sourcePath string,
	pipelineNames []string,
	debugOnly bool,
	concurrencyEnabled bool,
) error {
	config, err := config.NewConfigFromFile(configFile)
	if err != nil {
		return err
	}
	pipelines, err := config.GetPipelines(pipelineNames)
	if err != nil {
		return err
	}
	if debugOnly {
		fmt.Println("would execute:")
		for _, pipeline := range pipelines {
			targets := make([][]string, len(pipeline.Targets()))
			for i, stageTargets := range pipeline.Targets() {
				targets[i] = make([]string, len(stageTargets))
				for j, target := range stageTargets {
					targets[i][j] = target.Name()
				}
			}
			fmt.Printf("  %s targets: %s\n", pipeline.Name(), targets)
		}
		return nil
	}
	executionName := e.namer.NameSep("-")
	for _, pipeline := range pipelines {
		fmt.Printf("====> executing pipeline \"%s\" <====\n", pipeline.Name())
		pipelineExecutionName :=
			fmt.Sprintf("%s-%s", executionName, pipeline.Name())
		for i, stageTargets := range pipeline.Targets() {
			fmt.Printf("====> executing stage %d <====\n", i)
			stageExecutionName :=
				fmt.Sprintf("%s-stage%d", pipelineExecutionName, i)
			errCh := make(chan error)
			var runningTargets int
			for _, target := range stageTargets {
				targetExecutionName :=
					fmt.Sprintf("%s-%s", stageExecutionName, target.Name())
				runningTargets++
				go e.orchestrator.ExecuteTarget(
					ctx,
					targetExecutionName,
					sourcePath,
					target,
					errCh,
				)
				// If concurrency isn't enabled, wait for a potential error. If it's
				// nil, move on. If it's not, return the error.
				if !concurrencyEnabled {
					if err := <-errCh; err != nil {
						return err
					}
					runningTargets--
				}
			}
			// If concurrency is enabled, wait for all the targets to finish.
			if concurrencyEnabled {
				errs := []error{}
				for err := range errCh {
					if err != nil {
						errs = append(errs, err)
					}
					runningTargets--
					if runningTargets == 0 {
						break
					}
				}
				if len(errs) > 1 {
					return &multiError{errs: errs}
				}
				if len(errs) == 1 {
					return errs[0]
				}
			}
		}
	}
	return nil
}
