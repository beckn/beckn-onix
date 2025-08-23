#!/bin/bash

# Prompt for container name
echo "Please provide the container name for which you want to create the layer2config:"
read CONTAINER_NAME

# Validate container name is not empty
if [ -z "$CONTAINER_NAME" ]; then
    echo "Error: Container name cannot be empty"
    exit 1
fi

# Prompt for domain name
echo "Please provide the domain name for which you want to create the layer2config:"
read DOMAIN_NAME

# Validate domain name is not empty
if [ -z "$DOMAIN_NAME" ]; then
    echo "Error: Domain name cannot be empty"
    exit 1
fi

# Replace all occurrences of ':' with '_' in domain name
PROCESSED_DOMAIN=$(echo "$DOMAIN_NAME" | tr ':' '_')

# Create the final filename
FINAL_FILENAME="${PROCESSED_DOMAIN}_1.1.0.yaml"

# Execute the docker command
echo "Creating layer2 config file with name: $FINAL_FILENAME"
docker exec -it "$CONTAINER_NAME" cp schemas/core_1.1.0.yaml schemas/"$FINAL_FILENAME"

# Check if the command was successful
if [ $? -eq 0 ]; then
    echo "Successfully created $FINAL_FILENAME in container $CONTAINER_NAME"
else
    echo "Failed to create the file. Please check if the container exists and is running."
    exit 1
fi