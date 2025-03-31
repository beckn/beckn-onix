#!/bin/bash

# Exit immediately if any command fails
set -e

# Define Google Cloud Storage bucket
GCS_BUCKET="gs://beckn-onix-demo/plugins/"

# Define output directory for compiled plugins
PLUGIN_OUTPUT_DIR="./generated"

# Define plugin names from command-line arguments
PLUGIN_NAMES=("$@")

if [ $# -eq 0 ]; then
  echo "Error: No plugin names provided."
  exit 1
fi

# Define the zip file name
ZIP_FILE="plugins_bundle.zip"

# Remove existing output directory and recreate it
rm -rf "$PLUGIN_OUTPUT_DIR"
mkdir -p "$PLUGIN_OUTPUT_DIR"

# Build command for all plugins
BUILD_CMDS=""
for PLUGIN_NAME in "${PLUGIN_NAMES[@]}"; do
  BUILD_CMDS+="go build -buildmode=plugin -buildvcs=false -o ${PLUGIN_OUTPUT_DIR}/${PLUGIN_NAME}.so ./pkg/plugin/implementation/${PLUGIN_NAME}/cmd && "
done
BUILD_CMDS=${BUILD_CMDS%" && "}  # Remove trailing '&&'

echo "🚀 Building all plugins in a single Docker run..."

# Run a single Docker container to build all plugins
docker run --rm -v "$(pwd)":/app -w /app golang:1.24-bullseye sh -c "$BUILD_CMDS"

echo "✅ All plugins built successfully in $PLUGIN_OUTPUT_DIR"

# Zip all plugin files
echo "📦 Creating zip archive..."
cd "$PLUGIN_OUTPUT_DIR"
zip -r "../$ZIP_FILE" *.so
echo "✅ Created $ZIP_FILE"
cd ..

# Upload the zip file to GCS
if gsutil cp "$ZIP_FILE" "$GCS_BUCKET"; then
  echo "✅ Uploaded $ZIP_FILE to $GCS_BUCKET"
else
  echo "❌ Failed to upload $ZIP_FILE" >&2
  exit 1
fi

# Cleanup local files
rm -rf "$PLUGIN_OUTPUT_DIR" "$ZIP_FILE"
echo "🧹 Cleanup complete!"

echo "🎉 Plugin installation complete!"