#!/bin/bash

# Create plugins directory if it doesn't exist
mkdir -p plugins

# Build publisher plugin
go build -buildmode=plugin -o plugins/publisher.so ./plugins/publisher/main.go

# Build validator plugin
go build -buildmode=plugin -o plugins/validator.so ./plugins/validator/main.go

echo "Plugin build complete!"