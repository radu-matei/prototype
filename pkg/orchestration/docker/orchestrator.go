package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"

	dockerTypes "github.com/docker/docker/api/types"
	dockerContainer "github.com/docker/docker/api/types/container"
	docker "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/radu-matei/prototype/pkg/config"
	"github.com/radu-matei/prototype/pkg/orchestration"
	shellwords "github.com/mattn/go-shellwords"
	"github.com/pkg/errors"
	"gopkg.in/src-d/go-git.v4"
)

type devOrchestrator struct {
	dockerClient *docker.Client
}

func NewOrchestrator(dockerClient *docker.Client) orchestration.Orchestrator {
	return &devOrchestrator{
		dockerClient: dockerClient,
	}
}

func (d *devOrchestrator) ExecuteTarget(
	ctx context.Context,
	secrets []string,
	targetExecutionName string,
	sourcePath string,
	target config.Target,
	errCh chan<- error,
) {
	var err error
	containerIDs := make([]string, len(target.Containers()))

	// Ensure cleanup of all containers
	defer func() {
		d.forceRemoveContainers(context.Background(), containerIDs...)
		errCh <- err
	}()

	if len(target.Containers()) == 0 {
		return
	}

	fmt.Printf("----> executing target \"%s\" <----\n", target.Name())

	var networkContainerID, lastContainerID string
	var lastContainer config.Container
	// Create and start all containers-- except the last one-- that one we will
	// only create, then we will set ourselves up to capture its output and exit
	// code before we start it.
	for i, container := range target.Containers() {
		var containerID string
		if containerID, err = d.createContainer(
			ctx,
			secrets,
			targetExecutionName,
			sourcePath,
			networkContainerID,
			container,
		); err != nil {
			err = errors.Wrapf(
				err,
				"error creating container \"%s\" for target \"%s\"",
				container.Name(),
				target.Name(),
			)
			return
		}
		containerIDs[i] = containerID
		if i == 0 {
			networkContainerID = containerID
		}
		if i == len(containerIDs)-1 {
			lastContainerID = containerID
			lastContainer = container
		} else {
			// Start all but the last container
			if err = d.dockerClient.ContainerStart(
				ctx,
				containerID,
				dockerTypes.ContainerStartOptions{},
			); err != nil {
				err = errors.Wrapf(
					err,
					"error starting container \"%s\" for target \"%s\"",
					container.Name(),
					target.Name(),
				)
				return
			}
		}
	}
	// Establish channels to use for waiting for the last container to exit
	containerWaitRespCh, containerWaitErrCh := d.dockerClient.ContainerWait(
		ctx,
		lastContainerID,
		dockerContainer.WaitConditionNextExit,
	)
	// Attach to the last container to see its output
	var containerAttachResp dockerTypes.HijackedResponse
	if containerAttachResp, err = d.dockerClient.ContainerAttach(
		ctx,
		lastContainerID,
		dockerTypes.ContainerAttachOptions{
			Stream: true,
			Stdout: true,
			Stderr: true,
		},
	); err != nil {
		err = errors.Wrapf(
			err,
			"error attaching to container \"%s\" for target \"%s\"",
			lastContainer.Name(),
			target.Name(),
		)
		return
	}
	// Concurrently deal with the output from the last container
	go func() {
		defer containerAttachResp.Close()
		var gerr error
		stdOutWriter := prefixingWriter(
			target.Name(),
			lastContainer.Name(),
			os.Stdout,
		)
		if lastContainer.TTY() {
			_, gerr = io.Copy(stdOutWriter, containerAttachResp.Reader)
		} else {
			stdErrWriter := prefixingWriter(
				target.Name(),
				lastContainer.Name(),
				os.Stderr,
			)
			_, gerr = stdcopy.StdCopy(
				stdOutWriter,
				stdErrWriter,
				containerAttachResp.Reader,
			)
		}
		if gerr != nil {
			fmt.Printf(
				"error processing output from container \"%s\" for target \"%s\": %s\n",
				lastContainer.Name(),
				target.Name(),
				err,
			)
		}
	}()
	// Finally, start the last container
	if err = d.dockerClient.ContainerStart(
		ctx,
		lastContainerID,
		dockerTypes.ContainerStartOptions{},
	); err != nil {
		err = errors.Wrapf(
			err,
			"error starting container \"%s\" for target \"%s\"",
			lastContainer.Name(),
			target.Name(),
		)
		return
	}
	select {
	case containerWaitResp := <-containerWaitRespCh:
		if containerWaitResp.StatusCode != 0 {
			// The command executed inside the container exited non-zero
			err = &orchestration.ErrTargetExitedNonZero{
				Target:   target.Name(),
				ExitCode: containerWaitResp.StatusCode,
			}
			return
		}
	case err = <-containerWaitErrCh:
		err = errors.Wrapf(
			err,
			"error waiting for completion of container \"%s\" for target \"%s\"",
			lastContainer.Name(),
			target.Name(),
		)
		return
	case <-ctx.Done():
	}
}

// createContainer creates a container for the given execution and target,
// taking source path, any established networking, and container-specific
// configuration into account. It returns the newly created container's ID. It
// does not start the container.
func (d *devOrchestrator) createContainer(
	ctx context.Context,
	secrets []string,
	targetExecutionName string,
	sourcePath string,
	networkContainerID string,
	container config.Container,
) (string, error) {
	// TODO: We should probably move this somewhere else
	workDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	repo, err := git.PlainOpen(workDir)
	if err != nil {
		return "", err
	}
	ref, err := repo.Head()
	if err != nil {
		return "", err
	}
	// TODO: End "we should probably move this somewhere else"

	env := make([]string, len(secrets))
	copy(env, secrets)
	env = append(env, fmt.Sprintf("DRAKE_SHA1=%s", ref.Hash()))
	env = append(env, fmt.Sprintf("DRAKE_BRANCH=%s", ref.Name().Short()))
	env = append(env, "DRAKE_TAG=")

	containerConfig := &dockerContainer.Config{
		Image:        container.Image(),
		Env:          append(env, container.Environment()...),
		WorkingDir:   container.WorkingDirectory(),
		Tty:          container.TTY(),
		AttachStdout: true,
		AttachStderr: true,
	}
	if container.Command() != "" {
		var cmd []string
		cmd, err = shellwords.Parse(container.Command())
		if err != nil {
			return "", errors.Wrap(err, "error parsing container command")
		}
		containerConfig.Cmd = cmd
	}
	hostConfig := &dockerContainer.HostConfig{
		Privileged: container.Privileged(),
	}
	if networkContainerID != "" {
		hostConfig.NetworkMode = dockerContainer.NetworkMode(
			fmt.Sprintf("container:%s", networkContainerID),
		)
	}
	if container.MountDockerSocket() {
		hostConfig.Binds = []string{"/var/run/docker.sock:/var/run/docker.sock"}
	}
	if container.SourceMountPath() != "" {
		hostConfig.Binds = append(
			hostConfig.Binds,
			fmt.Sprintf("%s:%s", sourcePath, container.SourceMountPath()),
		)
	}
	fullContainerName := fmt.Sprintf(
		"%s-%s",
		targetExecutionName,
		container.Name(),
	)
	containerCreateResp, err := d.dockerClient.ContainerCreate(
		ctx,
		containerConfig,
		hostConfig,
		nil,
		fullContainerName,
	)
	if err != nil {
		return "",
			errors.Wrapf(
				err,
				"error creating container \"%s\"",
				fullContainerName,
			)
	}
	return containerCreateResp.ID, nil
}

func (d *devOrchestrator) forceRemoveContainers(
	ctx context.Context,
	containerIDs ...string,
) {
	for _, containerID := range containerIDs {
		if err := d.dockerClient.ContainerRemove(
			ctx,
			containerID,
			dockerTypes.ContainerRemoveOptions{
				Force: true,
			},
		); err != nil {
			// TODO: Maybe this isn't the best way to deal with this
			fmt.Printf(`error removing container "%s": %s`, containerID, err)
		}
	}
}

func prefixingWriter(
	targetName string,
	containerName string,
	output io.Writer,
) io.Writer {
	pipeReader, pipeWriter := io.Pipe()
	scanner := bufio.NewScanner(pipeReader)
	scanner.Split(bufio.ScanLines)
	go func() {
		for scanner.Scan() {
			fmt.Fprintf(output, "[%s-%s] ", targetName, containerName)
			output.Write(scanner.Bytes()) // nolint: errcheck
			fmt.Fprint(output, "\n")
		}
	}()
	return pipeWriter
}
