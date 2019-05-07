#!/usr/bin/env bash

# AVOID INVOKING THIS SCRIPT DIRECTLY -- USE `drake run publish-cli`

set -euox pipefail

go get -u github.com/tcnksm/ghr

set +x

echo "Publishing CLI binaries for commit $DRAKE_SHA1"

ghr -t $GITHUB_TOKEN -u lovethedrake -r prototype -c $DRAKE_SHA1 -delete $DRAKE_TAG ./bin/cli/
