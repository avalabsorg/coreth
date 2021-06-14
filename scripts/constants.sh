#!/usr/bin/env bash

# Set the PATHS
GOPATH="$(go env GOPATH)"

# Set default binary location
binary_path="$GOPATH/src/github.com/ava-labs/avalanchego/build/avalanchego-latest/plugins/evm"

# Avalabs docker hub
dockerhub_repo="avaplatform/avalanchego"

# Current branch
current_branch=${CURRENT_BRANCH:-$(git branch --show-current)}
echo "Using branch: ${current_branch}"

# Image build id
# Use an abbreviated version of the full commit to tag the image.

# WARNING: this will use the most recent commit even if there are un-committed changes present
coreth_commit="$(git --git-dir="$CORETH_PATH/.git" rev-parse HEAD)"
coreth_commit_id="${coreth_commit::8}"

build_image_id=${BUILD_IMAGE_ID:-"$avalanche_version-$coreth_commit_id"}
