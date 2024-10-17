#!/bin/bash
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
source scripts/variables.sh

# Function to log in and get the API key
get_api_key() {
    local login_url="$registry_url"
    local username="$1"
    local password="$2"

    # Call the login API and extract the api_key from the response
    local response=$(curl -s -H 'ACCEPT: application/json' \
                           -H 'CONTENT-TYPE: application/json' \
                           -d '{"User" : { "Name" : "'"$username"'", "Password" : "'"$password"'" } }' \
                           "$login_url")

    # Check if the curl command was successful
    if [ $? -ne 0 ]; then
        echo "${BoldRed}Error logging in to get API key${NC}"
        return 1
    fi

    # Extract the api_key from the response
    local api_key=$(echo "$response" | jq -r '.api_key')

    # Check if api_key is not null
    if [ "$api_key" == "null" ] || [ -z "$api_key" ]; then
        echo "${BoldRed}Failed to retrieve API key${NC}"
        return 1
    fi
}

# Function to upload the RolePermission.xlsx file
upload_role_permission() {
    local api_key="$1"
    local upload_url="$registry_url/role_permissions/importxls"

    # Use curl to upload the file
    curl -s -H "ApiKey:$api_key" \
         -F "datafile=@$REGISTRY_FILE_PATH" \
         "$upload_url"

    # Check if the curl command was successful
    if [ $? -ne 0 ]; then
        echo "${BoldRed}Error uploading RolePermission.xlsx${NC}"
        return 1
    fi
}


echo $REGISTRY_FILE_PATH

# Get the API key
API_KEY=$(get_api_key "$USERNAME" "$PASSWORD")
if [ $? -ne 0 ]; then
    echo "${BoldRed}Role permission update failed. Please upload Role Permission manually.${NC}"
else
    # Upload the file using the retrieved API key
    upload_role_permission "$API_KEY"
    if [ $? -ne 0 ]; then
        echo "${GREEN}Role permission updated in registry${NC}"
    fi
fi
