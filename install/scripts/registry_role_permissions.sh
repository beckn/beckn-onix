#!/bin/bash

# Set script directory and source variables
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/variables.sh"

API_KEY=""

# Function to log in and retrieve the API key
get_api_key() {
    local login_url="${registry_url%/subscribers}/login"
    local username="$1"
    local password="$2"
    local max_retries=20
    local retry_count=0
    local success=false

    while [ $retry_count -lt $max_retries ] && [ "$success" = false ]; do
        # Call the login API
        local response
        response=$(curl -s -H "Accept: application/json" \
                        -H "Content-Type: application/json" \
                        -d "{ \"Name\": \"${username}\", \"Password\": \"${password}\" }" \
                        "$login_url")

        # Check if curl failed
        if [ $? -ne 0 ]; then
            echo -e "${BoldRed}Error: Failed to connect to $login_url. Retrying in 5 seconds... (Attempt $((retry_count + 1)) of $max_retries)${NC}"
            retry_count=$((retry_count + 1))
            sleep 5
            continue
        fi

        # Extract API key using jq
        API_KEY=$(echo "$response" | jq -r '.api_key')
        
        # Validate API key
        if [[ -z "$API_KEY" || "$API_KEY" == "null" ]]; then
            echo -e "${BoldRed}Error: Failed to retrieve API key. Retrying in 5 seconds... (Attempt $((retry_count + 1)) of $max_retries)${NC}"
            retry_count=$((retry_count + 1))
            sleep 5
            continue
        fi

        success=true
        echo -e "${BoldGreen}API Key retrieved successfully${NC}"
        return 0
    done

    if [ "$success" = false ]; then
        echo -e "${BoldRed}Error: Failed to retrieve API key after $max_retries attempts${NC}"
        return 1
    fi
}

# Function to upload the RolePermission.xlsx file
upload_role_permission() {
    local api_key="$1"
    local login_url="${registry_url%/subscribers}/role_permissions/importxls"
    # Validate if file exists
    if [[ ! -f "$REGISTRY_FILE_PATH" ]]; then
        echo -e "${BoldRed}Error: File $REGISTRY_FILE_PATH not found${NC}"
        return 1
    fi
    # Upload the file 
    local response
    response=$(curl -s -w "%{http_code}" -o /dev/null -H "ApiKey:$api_key" \
                        -F "datafile=@${REGISTRY_FILE_PATH}" \
                        "$login_url")

    # # Check if curl failed
    if [ "$response" -ne 302 ]; then
        echo -e "${BoldRed}Error: Failed to upload RolePermission.xlsx. HTTP Status: $response${NC}"
        return 1
    fi
    echo -e "${BoldGreen}RolePermission.xlsx uploaded successfully${NC}"
    return 0
}

# Main Execution
REGISTRY_FILE_PATH=$SCRIPT_DIR/RolePermission.xlsx

if [[ $1 ]]; then
    registry_url=$1
else
    registry_url="http://localhost:3030"
fi

# Step 1: Get the API key
if ! get_api_key "$USERNAME" "$PASSWORD"; then
    echo -e "${BoldRed}Error: Role permission update failed. Please upload manually.${NC}"
    exit 1
fi

# Step 2: Upload the file
if upload_role_permission "$API_KEY"; then
    echo -e "${BoldGreen}Role permission updated in registry successfully.${NC}"
else
    echo -e "${BoldRed}Error: Role permission update failed.${NC}"
    exit 1
fi