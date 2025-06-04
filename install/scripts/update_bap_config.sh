#!/bin/bash
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

# Convert Windows paths to Unix-style paths if running in Git Bash
if [[ $(uname -s) == *"MINGW"* ]] || [[ $(uname -s) == *"MSYS"* ]] || [[ $(uname -s) == *"CYGWIN"* ]]; then
    SCRIPT_DIR=$(cygpath -u "$SCRIPT_DIR")
fi

source $SCRIPT_DIR/registry_entry.sh
source $SCRIPT_DIR/generate_keys.sh
source $SCRIPT_DIR/variables.sh
source $SCRIPT_DIR/get_container_details.sh

# Convert Windows paths to Unix-style paths if running in Git Bash
if [[ $(uname -s) == *"MINGW"* ]] || [[ $(uname -s) == *"MSYS"* ]] || [[ $(uname -s) == *"CYGWIN"* ]]; then
    bapClientFile=$(cygpath -u "$bapClientFile")
    bapNetworkFile=$(cygpath -u "$bapNetworkFile")
fi

newClientFile=$(echo "$bapClientFile" | sed 's/yaml-sample/yml/')
newNetworkFile=$(echo "$bapNetworkFile" | sed 's/yaml-sample/yml/')

cp "$bapClientFile" "$newClientFile"
cp "$bapNetworkFile" "$newNetworkFile"

clientFile=$newClientFile
networkFile=$newNetworkFile

client_port=$bap_client_port
network_port=$bap_network_port

if [[ $1 ]]; then
    registry_url=$1
    bap_subscriber_id=$2
    bap_subscriber_key_id=$3
    bap_subscriber_url=$4
    api_key=$5
    np_domain=$6
else
    if [[ $(uname -s) == *"MINGW"* ]] || [[ $(uname -s) == *"MSYS"* ]] || [[ $(uname -s) == *"CYGWIN"* ]]; then
        ip=localhost
        registry_url="http://$ip:3030/subscribers"
    elif [[ $(uname -s) == 'Darwin' ]]; then
        ip=localhost
        registry_url="http://$ip:3030/subscribers"
    elif [[ $(systemd-detect-virt) == 'wsl' ]]; then
        ip=$(hostname -I | awk '{print $1}')
        registry_url="http://$ip:3030/subscribers"
    else
        registry_url="http://$(get_container_ip registry):3030/subscribers"
    fi 
fi

if [[ $(uname) == "Darwin" ]]; then
    sed -i '' "s|BAP_NETWORK_PORT|$network_port|" $networkFile
    sed -i '' "s|BAP_CLIENT_PORT|$client_port|" $clientFile
else
    sed -i "s|BAP_NETWORK_PORT|$network_port|" $networkFile
    sed -i "s|BAP_CLIENT_PORT|$client_port|" $clientFile
fi

echo "Generating public/private key pair"
get_keys

if [[ $(uname -s ) == 'Darwin' ]];then
    valid_from=$(date -u -v-1d +"%Y-%m-%dT%H:%M:%S.%000Z")
    valid_until=$(date -u -v+3y +"%Y-%m-%dT%H:%M:%S.%000Z")
else
    valid_from=$(date -u -d "-1 day" +"%Y-%m-%dT%H:%M:%S.%3NZ")
    valid_until=$(date -u -d "+3 year" +"%Y-%m-%dT%H:%M:%S.%3NZ")
fi

type=BAP


# Define an associative array for replacements
if [[ $(uname -s ) == 'Darwin' ]];then
    replacements=(
        "REDIS_URL=$redisUrl"
        "REGISTRY_URL=$(if [[ $registry_url == *"localhost:3030"* ]]; then echo "http://registry:3030/subscribers"; else echo "$registry_url"; fi)"
        "MONGO_USERNAME=$mongo_initdb_root_username"
        "MONGO_PASSWORD=$mongo_initdb_root_password"
        "MONGO_DB_NAME=$mongo_initdb_database"
        "MONOG_URL=$mongoUrl"
        "RABBITMQ_USERNAME=$rabbitmq_default_user"
        "RABBITMQ_PASSWORD=$rabbitmq_default_pass"
        "RABBITMQ_URL=$rabbitmqUrl"
        "PRIVATE_KEY=$private_key"
        "PUBLIC_KEY=$public_key"
        "BAP_SUBSCRIBER_ID=$bap_subscriber_id"
        "BAP_SUBSCRIBER_URL=$bap_subscriber_url"
        "BAP_SUBSCRIBER_KEY_ID=$bap_subscriber_key_id"
        "USE_LAYER_2_CONFIG"=false
        "MANDATE_LAYER_2_CONFIG"=false
    )

    echo "Configuring BAP protocol server"
    # Apply replacements in both files
    for file in "$clientFile" "$networkFile"; do
        for line in "${replacements[@]}"; do
            key="${line%%=*}"
            value="${line#*=}"

            escaped_key=$(printf '%s\n' "$key" | sed 's/[]\/$*.^[]/\\&/g')
            escaped_value=$(printf '%s\n' "$value" | sed 's/[&/]/\\&/g')

            sed -i '' "s|$escaped_key|$escaped_value|g" "$file"
        done

    done
else
    declare -A replacements=(
        ["REDIS_URL"]=$redisUrl
        ["REGISTRY_URL"]=$(if [[ $registry_url == *"localhost:3030"* ]]; then echo "http://registry:3030/subscribers"; else echo "$registry_url"; fi)
        ["MONGO_USERNAME"]=$mongo_initdb_root_username
        ["MONGO_PASSWORD"]=$mongo_initdb_root_password
        ["MONGO_DB_NAME"]=$mongo_initdb_database
        ["MONOG_URL"]=$mongoUrl
        ["RABBITMQ_USERNAME"]=$rabbitmq_default_user
        ["RABBITMQ_PASSWORD"]=$rabbitmq_default_pass
        ["RABBITMQ_URL"]=$rabbitmqUrl
        ["PRIVATE_KEY"]=$private_key
        ["PUBLIC_KEY"]=$public_key
        ["BAP_SUBSCRIBER_ID"]=$bap_subscriber_id
        ["BAP_SUBSCRIBER_URL"]=$bap_subscriber_url
        ["BAP_SUBSCRIBER_KEY_ID"]=$bap_subscriber_key_id
        ["USE_LAYER_2_CONFIG"]=false
        ["MANDATE_LAYER_2_CONFIG"]=false        
    )

    echo "Configuring BAP protocol server"
    # Apply replacements in both files
    for file in "$clientFile" "$networkFile"; do
        for key in "${!replacements[@]}"; do
            sed -i "s|$key|${replacements[$key]}|" "$file"
        done
    done
fi

echo "Registering BAP protocol server on the registry"

create_network_participant "$registry_url" "application/json" "$bap_subscriber_id" "$bap_subscriber_key_id" "$bap_subscriber_url" "$public_key" "$public_key" "$valid_from" "$valid_until" "$type" "$api_key" "$np_domain"