#!/bin/bash

# Create plugins directory
mkdir -p plugins

# otelsetup imports pkg/version and is the only plugin that needs build-time
# identity embedded via -ldflags -X -- it's compiled as its own .so, a
# separate link unit from the main adapter binary, so without this it would
# silently keep pkg/version's "dev"/"unknown" defaults regardless of what
# version the adapter binary itself was built with. ONIX_VERSION/GIT_COMMIT/
# GIT_TREE_STATE/BUILD_DATE may already be set in the environment (e.g. by
# install/setup.sh, or as Docker build-args) -- version-vars.sh keeps
# whatever is already set and only computes from git for what's missing.
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
source "${SCRIPT_DIR}/scripts/version-vars.sh"

# Build each plugin as a shared library. Entries are "<source dir>" or
# "<source dir>:<output name>" — the .so basename is the plugin id used in
# config, so it can differ from the package directory (vcvalidator builds as
# validateVC.so to match the verb naming of pipeline steps).
echo "Building plugins..."

plugins=(
    "cache"
    "decrypter"
    "encrypter"
    "keymanager"
    "simplekeymanager"
    "publisher"
    "registry"
    "dediregistry"
    "manifestloader"
    "reqpreprocessor"
    "otelsetup"
    "reqmapper"
    "router"
    "schemavalidator"
    "schemav2validator"
    "signer"
    "signvalidator"
    "opapolicychecker"
    "payloadstore"
    "schemaversionmediator"
    "vcvalidator:validateVC"
)

for entry in "${plugins[@]}"; do
    plugin="${entry%%:*}"
    out="${entry#*:}"
    echo "Building $plugin plugin..."
    ldflags=()
    if [ "$plugin" = "otelsetup" ]; then
        ldflags=(-ldflags "${ONIX_LDFLAGS}")
    fi
    go build -buildmode=plugin "${ldflags[@]}" -o "plugins/${out}.so" "./pkg/plugin/implementation/${plugin}/cmd/plugin.go"
    if [ $? -eq 0 ]; then
        echo "✓ Successfully built $plugin plugin"
    else
        echo "✗ Failed to build $plugin plugin"
        exit 1
    fi
done

echo "All plugins built in ./plugins directory"
