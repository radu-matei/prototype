package orchestration

import (
	"fmt"
)

type ErrTargetExitedNonZero struct {
	Target   string
	ExitCode int64
}

func (e *ErrTargetExitedNonZero) Error() string {
	return fmt.Sprintf(
		`target "%s" failed with non-zero exit code %d`,
		e.Target,
		e.ExitCode,
	)
}
