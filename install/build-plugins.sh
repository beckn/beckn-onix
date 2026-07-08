#!/bin/bash

# Create plugins directory
mkdir -p plugins

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
    go build -buildmode=plugin -o "plugins/${out}.so" "./pkg/plugin/implementation/${plugin}/cmd/plugin.go"
    if [ $? -eq 0 ]; then
        echo "✓ Successfully built $plugin plugin"
    else
        echo "✗ Failed to build $plugin plugin"
        exit 1
    fi
done

echo "All plugins built in ./plugins directory"
