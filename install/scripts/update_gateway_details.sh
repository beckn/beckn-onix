#!/bin/bash
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
source $SCRIPT_DIR/get_container_details.sh

gateway_id=gateway
gateway_port=4030
protocol=http
reg_url=http://$1:3030/subscribers/lookup
registry_id=registry
registry_url=http://registry:3030

# Function to ensure directory structure exists
ensure_directory_structure() {
    local base_dir="$SCRIPT_DIR/../gateway_data"
    local config_dir="$base_dir/config"
    local networks_dir="$config_dir/networks"
    
    # Create directories if they don't exist
    mkdir -p "$config_dir"
    mkdir -p "$networks_dir"
    
    # Create sample files if they don't exist
    if [[ ! -f "$config_dir/envvars" ]]; then
        echo "Creating envvars file..."
        echo "JAVA_OPTS=-Xmx512m" > "$config_dir/envvars"
    fi
    
    if [[ ! -f "$config_dir/logger.properties" ]]; then
        echo "Creating logger.properties file..."
        echo "handlers=java.util.logging.ConsoleHandler
.level=INFO
java.util.logging.ConsoleHandler.level=INFO
java.util.logging.ConsoleHandler.formatter=java.util.logging.SimpleFormatter" > "$config_dir/logger.properties"
    fi
    
    if [[ ! -f "$config_dir/swf.properties-sample" ]]; then
        echo "Creating swf.properties-sample file..."
        echo "subscriber_id=SUBSCRIBER_ID
gateway_url=GATEWAY_URL
gateway_port=GATEWAY_PORT
protocol=PROTOCOL
registry_url=REGISTRY_URL" > "$config_dir/swf.properties-sample"
    fi
    
    if [[ ! -f "$networks_dir/onix.json-sample" ]]; then
        echo "Creating onix.json-sample file..."
        echo '{
    "gateway_id": "GATEWAY_ID",
    "registry_id": "REGISTRY_ID",
    "registry_url": "REGISTRY_URL"
}' > "$networks_dir/onix.json-sample"
    fi
}

update_network_json(){
    ensure_directory_structure
    
    # Debug output before copying
    echo "Current directory: $(pwd)"
    echo "Script directory: $SCRIPT_DIR"
    echo "Networks directory: $SCRIPT_DIR/../gateway_data/config/networks"
    
    # Ensure networks directory exists
    mkdir -p "$SCRIPT_DIR/../gateway_data/config/networks"
    
    # Copy and update the network configuration
    cp "$SCRIPT_DIR/../gateway_data/config/networks/onix.json-sample" "$SCRIPT_DIR/../gateway_data/config/networks/onix.json"
    networks_config_file="$SCRIPT_DIR/../gateway_data/config/networks/onix.json"
    tmp_file=$(mktemp "tempfile.XXXXXXXXXX")
    sed " s|GATEWAY_ID|$gateway_id|g; s|REGISTRY_ID|$registry_id|g; s|REGISTRY_URL|$registry_url|g" "$networks_config_file" > "$tmp_file"
    mv "$tmp_file" "$networks_config_file"
    
    # Convert Windows paths to Unix-style paths if running in Git Bash
    if [[ $(uname -s) == *"MINGW"* ]] || [[ $(uname -s) == *"MSYS"* ]] || [[ $(uname -s) == *"CYGWIN"* ]]; then
        CONFIG_DIR=$(cd "$SCRIPT_DIR/../gateway_data/config" && pwd)
        CONFIG_DIR=$(cygpath -u "$CONFIG_DIR")
    else
        CONFIG_DIR="$SCRIPT_DIR/../gateway_data/config"
    fi
    
    # Debug output
    echo "Using configuration directory: $CONFIG_DIR"
    echo "Contents of networks directory:"
    ls -la "$CONFIG_DIR/networks"
    
    # Create networks directory in the target if it doesn't exist
    docker run --rm -v "$CONFIG_DIR:/source" -v gateway_data_volume:/target busybox sh -c "mkdir -p /target/networks && cp -r /source/networks/* /target/networks/"
}

get_details_registry() {
    # Make the curl request and store the output in a variable
    response=$(curl --location --request POST "$reg_url" \
        --header 'Content-Type: application/json' \
        --data-raw '{
    "type": "LREG"
}')
    # Check if the curl command was successful (HTTP status code 2xx)
    if [ $? -eq 0 ]; then
        # Extract signing_public_key and encr_public_key using jq
        signing_public_key=$(echo "$response" | jq -r '.[0].signing_public_key')
        encr_public_key=$(echo "$response" | jq -r '.[0].encr_public_key')
        subscriber_url=$(echo "$response" | jq -r '.[0].subscriber_url')

    else
        echo "Error: Unable to fetch data from the server."
    fi
}

update_gateway_config() {
        ensure_directory_structure
        
        cp $SCRIPT_DIR/../gateway_data/config/swf.properties-sample $SCRIPT_DIR/../gateway_data/config/swf.properties
        config_file="$SCRIPT_DIR/../gateway_data/config/swf.properties"
        
        tmp_file=$(mktemp "tempfile.XXXXXXXXXX")
        sed " s|SUBSCRIBER_ID|$gateway_id|g; s|GATEWAY_URL|$gateway_id|g; s|GATEWAY_PORT|$gateway_port|g; s|PROTOCOL|$protocol|g; s|REGISTRY_URL|$subscriber_url|g" "$config_file" > "$tmp_file"
        mv "$tmp_file" "$config_file"
        
        docker volume create gateway_data_volume
        docker volume create gateway_database_volume
        
        # Convert Windows paths to Unix-style paths if running in Git Bash
        if [[ $(uname -s) == *"MINGW"* ]] || [[ $(uname -s) == *"MSYS"* ]] || [[ $(uname -s) == *"CYGWIN"* ]]; then
            CONFIG_DIR=$(cd "$SCRIPT_DIR/../gateway_data/config" && pwd)
            CONFIG_DIR=$(cygpath -u "$CONFIG_DIR")
        else
            CONFIG_DIR="$SCRIPT_DIR/../gateway_data/config"
        fi
        
        # Debug output
        echo "Using configuration directory: $CONFIG_DIR"
        echo "Files to be copied:"
        ls -l "$CONFIG_DIR"/{envvars,logger.properties,swf.properties}
        
        # Copy files individually using sh -c
        docker run --rm -v "$CONFIG_DIR:/source" -v gateway_data_volume:/target busybox sh -c "cp /source/envvars /target/ && cp /source/logger.properties /target/ && cp /source/swf.properties /target/"
        
        update_network_json
}

# if [[ $1 == https://* ]]; then
#     reg_url=$1/subscribers/lookup
#     get_details_registry $reg_url
# else
#     service_name=$1
#     if [[ $(uname -s) == 'Darwin' ]]; then
#         ip=localhost
#     elif [[ $(systemd-detect-virt) == 'wsl' ]]; then
#         ip=$(hostname -I | awk '{print $1}')
#     else
#         ip=$(get_container_ip $service_name)
#     fi
#     reg_url=http://$ip:3030/subscribers/lookup
#     get_details_registry $reg_url
# fi

echo "Registry: $1 && Gateway: $2" 

if [[ $1 ]]; then
    registry_url=$1
    if [[ $1 == https://* ]]; then
        if [[ $(uname -s) == 'Darwin' ]]; then
            registry_id=$(echo "$1" | sed -E 's/https:\/\///')
        else
            registry_id=$(echo "$1" | sed 's/https:\/\///')
        fi
    elif [[ $1 == http://* ]]; then
        if [[ $(uname -s) == 'Darwin' ]]; then
            registry_id=$(echo "$1" | sed -E 's/http:\/\///')
        else
            registry_id=$(echo "$1" | sed 's/http:\/\///')
        fi
    fi
    if [[ $registry_id = "registry:3030" ]]; then
        registry_id="registry"
    fi
fi

if [[ $2 ]]; then
    if [[ $2 == https://* ]]; then
        if [[ $(uname -s) == 'Darwin' ]]; then
            gateway_id=$(echo "$2" | sed -E 's/https:\/\///')
        else
            gateway_id=$(echo "$2" | sed 's/https:\/\///')
        fi
        gateway_port=443
        protocol=https
        update_gateway_config
    elif [[ $2 == http://* ]]; then
        if [[ $(uname -s) == 'Darwin' ]]; then
            gateway_id=$(echo "$2" | sed -E 's/http:\/\///')
        else
            gateway_id=$(echo "$2" | sed 's/http:\/\///')
        fi
        gateway_port=80
        protocol=http
        update_gateway_config
    fi
else
    update_gateway_config
fi