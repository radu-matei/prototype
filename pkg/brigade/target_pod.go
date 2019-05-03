package brigade

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/lovethedrake/prototype/pkg/config"
	"github.com/mattn/go-shellwords"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	api "k8s.io/kubernetes/pkg/apis/core"
)

const (
	srcVolumeName          = "src"
	dockerSocketVolumeName = "docker-socket"
)

func (e *executor) runTargetPod(
	ctx context.Context,
	project Project,
	event Event,
	pipelineName string,
	stage int,
	target config.Target,
	environment []string,
	errCh chan<- error,
) {
	var err error
	if err = notifyCheckStart(event, target.Name(), target.Name()); err != nil {
		errCh <- err
		return
	}

	jobName := fmt.Sprintf("%s-stage%d-%s", pipelineName, stage, target.Name())
	podName := fmt.Sprintf("%s-%s", jobName, event.BuildID)

	// Ensure notification
	defer func() {
		conclusion := "failure"
		select {
		case <-ctx.Done():
			conclusion = "cancelled"
		default:
			if err == nil {
				conclusion = "success"
			} else if _, ok := err.(*timedOutError); ok {
				conclusion = "timed_out"
			}
		}
		if nerr := notifyCheckCompleted(
			event,
			target.Name(),
			target.Name(),
			conclusion,
		); nerr != nil {
			log.Printf("error sending notification to github: %s", nerr)
		}
		errCh <- err
	}()

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: podName,
			Labels: map[string]string{
				"heritage":             "brigade",
				"component":            "job",
				"jobname":              jobName,
				"project":              project.ID,
				"worker":               event.WorkerID,
				"build":                event.BuildID,
				"thedrake.io/pipeline": pipelineName,
				"thedrake.io/stage":    strconv.Itoa(stage),
				"thedrake.io/target":   target.Name(),
			},
		},
		Spec: v1.PodSpec{
			RestartPolicy: v1.RestartPolicyNever,
			Containers:    []v1.Container{},
			Volumes: []v1.Volume{
				{
					Name: srcVolumeName,
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: srcPVCName(event.WorkerID, pipelineName),
						},
					},
				},
				{
					Name: dockerSocketVolumeName,
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: "/var/run/docker.sock",
						},
					},
				},
			},
		},
	}
	var mainContainerName string
	containers := target.Containers()
	for i, container := range containers {
		var targetPodContainer v1.Container
		targetPodContainer, err = getTargetPodContainer(
			project,
			event,
			container,
			environment,
		)
		if err != nil {
			return
		}
		// We'll treat all but the last container as sidecars. i.e. The last
		// container in the target should be container 0 in the pod spec.
		if i < len(containers)-1 {
			pod.Spec.Containers = append(pod.Spec.Containers, targetPodContainer)
			continue
		}
		// This is the primary container. Make it the first (0th) in the pod spec.
		mainContainerName = container.Name()
		pod.Spec.Containers = append(
			[]v1.Container{targetPodContainer},
			pod.Spec.Containers...,
		)
	}

	if _, err = e.kubeClient.CoreV1().Pods(
		project.Kubernetes.Namespace,
	).Create(pod); err != nil {
		err = errors.Wrapf(err, "error creating pod \"%s\"", podName)
		return
	}

	var podsWatcher watch.Interface
	podsWatcher, err =
		e.kubeClient.CoreV1().Pods(project.Kubernetes.Namespace).Watch(
			metav1.ListOptions{
				FieldSelector: fields.OneTermEqualSelector(
					api.ObjectNameField,
					podName,
				).String(),
			},
		)
	if err != nil {
		return
	}

	// Timeout
	// TODO: This probably should not be hard-coded
	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()

	for {
		select {
		case event := <-podsWatcher.ResultChan():
			pod, ok := event.Object.(*v1.Pod)
			if !ok {
				err = errors.Errorf(
					"received unexpected object when watching pod \"%s\" for completion",
					podName,
				)
				return
			}
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.Name == mainContainerName {
					if containerStatus.State.Terminated != nil {
						if containerStatus.State.Terminated.Reason == "Completed" {
							return
						}
						err = errors.Errorf("pod \"%s\" failed", podName)
						return
					}
					break
				}
			}
		case <-timer.C:
			err = &timedOutError{podName: podName}
			return
		case <-ctx.Done():
			return
		}
	}
}

func getTargetPodContainer(
	project Project,
	event Event,
	container config.Container,
	environment []string,
) (v1.Container, error) {
	privileged := container.Privileged()
	command, err := shellwords.Parse(container.Command())
	if err != nil {
		return v1.Container{}, err
	}
	env := make([]string, len(environment))
	copy(env, environment)
	env = append(env, container.Environment()...)
	c := v1.Container{
		Name:            container.Name(),
		Image:           container.Image(),
		ImagePullPolicy: v1.PullAlways,
		Command:         command,
		Env:             []v1.EnvVar{},
		SecurityContext: &v1.SecurityContext{
			Privileged: &privileged,
		},
		VolumeMounts: []v1.VolumeMount{},
		Stdin:        container.TTY(),
		TTY:          container.TTY(),
	}
	for k := range project.Secrets {
		c.Env = append(
			c.Env,
			v1.EnvVar{
				Name: k,
				ValueFrom: &v1.EnvVarSource{
					SecretKeyRef: &v1.SecretKeySelector{
						LocalObjectReference: v1.LocalObjectReference{
							Name: strings.ToLower(event.BuildID),
						},
						Key: k,
					},
				},
			},
		)
	}
	for _, kv := range env {
		kvTokens := strings.SplitN(kv, "=", 2)
		if len(kvTokens) == 2 {
			c.Env = append(
				c.Env,
				v1.EnvVar{
					Name:  kvTokens[0],
					Value: kvTokens[1],
				},
			)
			continue
		}
		if len(kvTokens) == 1 {
			c.Env = append(
				c.Env,
				v1.EnvVar{
					Name: kvTokens[0],
				},
			)
		}
	}
	if container.SourceMountPath() != "" {
		c.VolumeMounts = append(
			c.VolumeMounts,
			v1.VolumeMount{
				Name:      srcVolumeName,
				MountPath: container.SourceMountPath(),
			},
		)
	}
	if container.WorkingDirectory() != "" {
		c.WorkingDir = container.WorkingDirectory()
	}
	if container.MountDockerSocket() {
		c.VolumeMounts = append(
			c.VolumeMounts,
			v1.VolumeMount{
				Name:      dockerSocketVolumeName,
				MountPath: "/var/run/docker.sock",
			},
		)
	}
	return c, nil
}
