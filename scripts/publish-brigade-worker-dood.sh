#!/bin/sh

# AVOID INVOKING THIS SCRIPT DIRECTLY -- USE `drake run publish-brigade-worker-dood

set -euo pipefail

if [ "$DRAKE_TAG" == "" ]; then
  rel_version=edge
else
  rel_version=$DRAKE_TAG
fi

git_version=$(git describe --always --abbrev=7 --dirty)

base_image_name=lovethedrake/prototype-brigade-worker

set -x

docker push $base_image_name:$git_version
docker push $base_image_name:$rel_version 
