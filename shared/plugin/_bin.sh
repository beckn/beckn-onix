#!/bin/bash

# Create plugins directory if it doesn't exist
mkdir -p plugins

# Build order plugin
if [ -d "plugins/order" ]; then
    echo "Building order plugin..."
    cd plugins/order
    go build -buildmode=plugin -o ./order.so
    cd ../..
fi


# Check if publisher plugin exists
if [ -d "plugins/publisher" ]; then
    echo "Building publisher plugin..."
    cd plugins/publisher
    go build -buildmode=plugin -o ./publisher.so
    cd ../..
fi

echo "Plugin build complete!"