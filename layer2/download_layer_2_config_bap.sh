#!/bin/bash

read -p "Enter the path from where to download the layer 2 configuration file:" FILE_URL

CONTAINER_NAME="bap-network"
CONTAINER_ID=`docker ps -aqf "name=$CONTAINER_NAME"`
CONTAINER_PATH="/usr/src/app/schemas"
wget -O "$(basename "$FILE_URL")" "$FILE_URL"

echo 

if [ $? -eq 0 ]; then
    echo "File downloaded successfully."
    FILENAME="$(basename "$FILE_URL")"
    CONTAINER_NAME="bap-network"
    CONTAINER_ID=`docker ps -aqf "name=$CONTAINER_NAME"`
    CONTAINER_PATH="/usr/src/app/schemas"

    docker cp "$FILENAME" "$CONTAINER_NAME":"$CONTAINER_PATH/$FILENAME"
    if [ $? -eq 0 ]; then
        echo "File copied to Docker container $CONTAINER_NAME successfully."
    fi

    CONTAINER_NAME="bap-client"
    CONTAINER_ID=`docker ps -aqf "name=$CONTAINER_NAME"`

    docker cp "$FILENAME" "$CONTAINER_NAME":"$CONTAINER_PATH/$FILENAME"

    if [ $? -eq 0 ]; then
        echo "File copied to Docker container $CONTAINER_NAME successfully."
        rm "$(basename "$FILE_URL")"
        if [ $? -eq 0 ]; then
            echo "Local copy of the file deleted successfully."
        else
            echo "Failed to delete the local copy of the file."
        fi
    else
        echo "Failed to copy the file to Docker container."
    fi
else
    echo "Failed to download the file."
fi