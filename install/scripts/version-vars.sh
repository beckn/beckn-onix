#!/bin/bash
#
# Single source of truth for the adapter's build-time identity vars (see
# pkg/version). Source this file to populate ONIX_VERSION, GIT_COMMIT,
# GIT_TREE_STATE, BUILD_DATE, and ONIX_LDFLAGS.
#
# When a usable .git is present (local dev, CI checkout), these are always
# recomputed fresh from git -- re-sourcing this file mid-session always
# reflects the current HEAD, even if a previous source in the same shell
# left ONIX_VERSION etc. exported with an older value.
#
# When no .git is present (inside a Docker build stage, whose build context
# never includes .git), whatever is already set in the environment is kept
# as-is instead of being overwritten with git-lookup failures -- this is how
# the Dockerfiles' ARG-passed values survive being threaded through to
# install/build-plugins.sh.

if git rev-parse --git-dir >/dev/null 2>&1; then
    ONIX_VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
    GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"

    if [ -z "$(git status --porcelain 2>/dev/null)" ]; then
        GIT_TREE_STATE="clean"
    else
        GIT_TREE_STATE="dirty"
    fi

    # Derived from the commit's own timestamp, not wall-clock, so building
    # the same commit twice produces an identical value -- and therefore a
    # cacheable Docker layer -- instead of a fresh timestamp on every build.
    COMMIT_EPOCH="$(git show -s --format=%ct HEAD 2>/dev/null || echo 0)"
    BUILD_DATE="$(date -u -d "@${COMMIT_EPOCH}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -r "${COMMIT_EPOCH}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)"
else
    : "${ONIX_VERSION:=dev}"
    : "${GIT_COMMIT:=unknown}"
    : "${GIT_TREE_STATE:=unknown}"
    : "${BUILD_DATE:=unknown}"
fi

ONIX_VERSION_PKG="github.com/beckn-one/beckn-onix/pkg/version"
ONIX_LDFLAGS="-X ${ONIX_VERSION_PKG}.Version=${ONIX_VERSION} -X ${ONIX_VERSION_PKG}.GitCommit=${GIT_COMMIT} -X ${ONIX_VERSION_PKG}.GitTreeState=${GIT_TREE_STATE} -X ${ONIX_VERSION_PKG}.BuildDate=${BUILD_DATE}"
