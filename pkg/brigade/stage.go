package brigade

import (
	"context"
	"log"

	"github.com/lovethedrake/prototype/pkg/config"
)

func (e *executor) runStage(
	ctx context.Context,
	project Project,
	event Event,
	pipelineName string,
	stageIndex int,
	targets []config.Target,
) error {
	log.Printf("executing pipeline \"%s\" stage %d", pipelineName, stageIndex)
	errCh := make(chan error)
	var runningTargets int
	for _, target := range targets {
		log.Printf(
			"executing pipeline \"%s\" stage %d target \"%s\"",
			pipelineName,
			stageIndex,
			target.Name(),
		)
		runningTargets++
		go e.runTargetPod(
			ctx,
			project,
			event,
			pipelineName,
			stageIndex,
			target,
			errCh,
		)
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
