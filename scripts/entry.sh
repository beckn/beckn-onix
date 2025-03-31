#!/bin/sh


ARTIFACT_REGISTRY="${ARTIFACT_REGISTRY:-asia-southeast1}"
REPOSITORY="${REPOSITORY:-onix-plugins}"
PACKAGE="${PACKAGE:-bpp}"
VERSION="${VERSION:-v0.1.0}"
DEST_DIR="${DEST_DIR:-/app/plugins}"


# Authenticate with Artifact Registry (only needed if running locally or outside Cloud Run)
if [[ -n "$GOOGLE_APPLICATION_CREDENTIALS" ]]; then
  gcloud auth activate-service-account --key-file="$GOOGLE_APPLICATION_CREDENTIALS"
fi

# Download the latest plugin bundle from Artifact Registry
echo "🚀 Downloading plugin bundle from Artifact Registry..."
gcloud artifacts generic download "$PACKAGE" \
  --location="$ARTIFACT_REGISTRY" \
  --repository="$REPOSITORY" \
  --version="$VERSION" \
  --destination=plugins_bundle.tar.xz

echo "✅ Download complete!"

# Ensure the destination directory exists
mkdir -p "$DEST_DIR"

# Extract the archive
echo "📦 Extracting plugins..."
tar -xJf plugins_bundle.tar.xz -C "$DEST_DIR"

echo "✅ Plugins extracted to $DEST_DIR"

echo "✅ Starting server..."
exec /app/server --config="${CONFIG_FILE}"
