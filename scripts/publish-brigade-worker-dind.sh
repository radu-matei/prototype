#!/bin/sh

# AVOID INVOKING THIS SCRIPT DIRECTLY -- USE `drake run publish-brigade-worker-dind`

set -euo pipefail

if [ "$DRAKE_TAG" == "" ]; then
  rel_version=edge
else
  rel_version=$DRAKE_TAG
fi

git_version=$(git describe --always --abbrev=7 --dirty)
base_image_name=radu-matei/prototype-brigade-worker

dockerd_logs=$(mktemp)

function dumpDockerdLogs {
  set +x
  echo "---------- Dumping dockerd logs ----------"
  cat $dockerd_logs
}

trap dumpDockerdLogs EXIT

set -x

dockerd \
  --host=unix:///var/run/docker.sock \
  --host=tcp://0.0.0.0:2375 \
  &> $dockerd_logs &

# Wait for the containerized dockerd to be ready
scripts/wupiao.sh localhost 2375 300

set +x # Don't let the value of $DOCKER_PASSWORD bleed into the logs!
docker login -u krancour -p $DOCKER_PASSWORD
set -x

docker build . -t $base_image_name:$git_version
docker tag $base_image_name:$git_version $base_image_name:$rel_version

docker push $base_image_name:$git_version
docker push $base_image_name:$rel_version 
