#!/bin/bash

# Exit on error
set -e

# Define Artifact Registry info
ARTIFACT_REGISTRY="asia-southeast1-docker.pkg.dev/ondc-seller-dev/onix-plugins"
PLUGIN_BUNDLE="plugins_bundle.tar.xz"
DEST_DIR="./generated/plugins"

# Define plugin names from command-line arguments
PLUGIN_NAMES=("$@")

if [ $# -eq 0 ]; then
  echo "Error: No plugin names provided."
  exit 1
fi

# Ensure clean destination directory
rm -rf "$DEST_DIR"
mkdir -p "$DEST_DIR"

# Build command for all plugins
BUILD_CMDS=""
for PLUGIN_NAME in "${PLUGIN_NAMES[@]}"; do
  BUILD_CMDS+="go build -buildmode=plugin -buildvcs=false -o ${DEST_DIR}/${PLUGIN_NAME}.so ./plugin/${PLUGIN_NAME}/cmd && "
done
BUILD_CMDS=${BUILD_CMDS%" && "}  # Remove trailing '&&'

echo "🚀 Building all plugins in a single Docker run..."

# Run a single Docker container to build all plugins
docker run --rm -v "$(pwd)":/app -w /app golang:1.24-bullseye sh -c "$BUILD_CMDS"

echo "✅ All plugins built successfully in $PLUGIN_OUTPUT_DIR"


# Package the plugins as a compressed tar archive
echo "📦 Creating tar archive: $PLUGIN_BUNDLE..."
tar -cJf "$PLUGIN_BUNDLE" -C "$DEST_DIR" .
echo "✅ Archive created!"

# # Upload to Artifact Registry
# echo "🚀 Uploading to Artifact Registry..."

# gcloud artifacts generic upload \
# --location=asia-southeast1 \
# --repository=onix-plugins \
# --project=ondc-seller-dev \
# --package=bpp \
# --version=v0.1.0 \
# --source=plugins_bundle.tar.xz

# echo "✅ Uploaded to Artifact Registry: $ARTIFACT_REGISTRY/$PLUGIN_BUNDLE"
