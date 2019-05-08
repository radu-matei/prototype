package orchestration

import (
	"context"

	"github.com/radu-matei/prototype/pkg/config"
)

type Orchestrator interface {
	ExecuteTarget(
		ctx context.Context,
		secrets []string,
		executionName string,
		sourcePath string,
		target config.Target,
		errCh chan<- error,
	)
}
