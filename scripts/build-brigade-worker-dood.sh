#!/bin/sh

# AVOID INVOKING THIS SCRIPT DIRECTLY -- USE `drake run build-brigade-worker-dood

set -euo pipefail

if [ "$DRAKE_TAG" == "" ]; then
  rel_version=edge
else
  rel_version=$DRAKE_TAG
fi

git_version=$(git describe --always --abbrev=7 --dirty)

base_image_name=radu-matei/prototype-brigade-worker

set -x

docker build . -t $base_image_name:$git_version
docker tag $base_image_name:$git_version $base_image_name:$rel_version
