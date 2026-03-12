#!/bin/bash

# Create plugins directory
mkdir -p plugins

# Build each plugin as a shared library
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
    "reqpreprocessor"
    "otelsetup"
    "reqmapper"
    "router"
    "schemavalidator"
    "schemav2validator"
    "signer"
    "signvalidator"
)

for plugin in "${plugins[@]}"; do
    echo "Building $plugin plugin..."
    CGO_ENABLED=1 go build -buildmode=plugin -o "plugins/${plugin}.so" "./pkg/plugin/implementation/${plugin}/cmd/plugin.go"
    if [ $? -eq 0 ]; then
        echo "✓ Successfully built $plugin plugin"
    else
        echo "✗ Failed to build $plugin plugin"
        exit 1
    fi
done

echo "All plugins built in ./plugins directory"
