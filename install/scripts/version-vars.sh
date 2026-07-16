#!/bin/bash
#
# Single source of truth for the adapter's build-time identity vars (see
# pkg/version). Source this file to populate ONIX_VERSION, GIT_COMMIT,
# GIT_TREE_STATE, BUILD_DATE, and ONIX_LDFLAGS.
#
# Any of the four vars may already be set in the environment before this
# file is sourced (e.g. passed in as Docker build-args, since a Docker
# build stage has no .git to compute them from) -- in that case the
# existing value is kept as-is and the corresponding git command is
# skipped entirely.

: "${ONIX_VERSION:=$(git describe --tags --always 2>/dev/null || echo dev)}"
: "${GIT_COMMIT:=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"

if [ -z "${GIT_TREE_STATE:-}" ]; then
    if [ -z "$(git status --porcelain 2>/dev/null)" ]; then
        GIT_TREE_STATE="clean"
    else
        GIT_TREE_STATE="dirty"
    fi
fi

if [ -z "${BUILD_DATE:-}" ]; then
    # Derived from the commit's own timestamp, not wall-clock, so building
    # the same commit twice produces an identical value -- and therefore a
    # cacheable Docker layer -- instead of a fresh timestamp on every build.
    COMMIT_EPOCH="$(git show -s --format=%ct HEAD 2>/dev/null || echo 0)"
    BUILD_DATE="$(date -u -d "@${COMMIT_EPOCH}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -r "${COMMIT_EPOCH}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)"
fi

ONIX_VERSION_PKG="github.com/beckn-one/beckn-onix/pkg/version"
ONIX_LDFLAGS="-X ${ONIX_VERSION_PKG}.Version=${ONIX_VERSION} -X ${ONIX_VERSION_PKG}.GitCommit=${GIT_COMMIT} -X ${ONIX_VERSION_PKG}.GitTreeState=${GIT_TREE_STATE} -X ${ONIX_VERSION_PKG}.BuildDate=${BUILD_DATE}"
