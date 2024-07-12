#!/bin/bash
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

source $SCRIPT_DIR/registry_entry.sh
source $SCRIPT_DIR/generate_keys.sh
source $SCRIPT_DIR/variables.sh
source $SCRIPT_DIR/get_container_details.sh


newClientFile=$(echo "$bppClientFile" | sed 's/yaml-sample/yml/')
newNetworkFile=$(echo "$bppNetworkFile" | sed 's/yaml-sample/yml/')

cp $bppClientFile $newClientFile
cp $bppNetworkFile $newNetworkFile

clientFile=$newClientFile
networkFile=$newNetworkFile

client_port=$bpp_client_port
network_port=$bpp_network_port

if [[ $(uname) == "Darwin" ]]; then
    sed -i '' "s|BPP_NETWORK_PORT|$network_port|" $networkFile
    sed -i '' "s|BPP_CLIENT_PORT|$client_port|" $clientFile
else
    sed -i "s|BPP_NETWORK_PORT|$network_port|" $networkFile
    sed -i "s|BPP_CLIENT_PORT|$client_port|" $clientFile
fi 

if [[ $1 ]]; then
    registry_url=$1
    bpp_subscriber_id=$2
    bpp_subscriber_key_id=$3
    bpp_subscriber_url=$4
    webhook_url=$5
else
    if [[ $(uname -s) == 'Darwin' ]]; then
        ip=localhost
        registry_url="http://$ip:3030/subscribers"
    elif [[ $(systemd-detect-virt) == 'wsl' ]]; then
        ip=$(hostname -I | awk '{print $1}')
        registry_url="http://$ip:3030/subscribers"
    else
        registry_url="http://$(get_container_ip registry):3030/subscribers"
    fi 
fi

echo "Generating public/private key pair"
get_keys
#echo "Your Private Key: $private_key" 
#echo "Your Public Key: $public_key"


if [[ $(uname -s ) == 'Darwin' ]];then
    valid_from=$(date -u -v-1d +"%Y-%m-%dT%H:%M:%S.%000Z")
    valid_until=$(date -u -v+3y +"%Y-%m-%dT%H:%M:%S.%000Z")
else
    valid_from=$(date -u -d "-1 day" +"%Y-%m-%dT%H:%M:%S.%3NZ")
    valid_until=$(date -u -d "+3 year" +"%Y-%m-%dT%H:%M:%S.%3NZ")
fi

type=BPP


# Define an associative array for replacements
if [[ $(uname -s ) == 'Darwin' ]];then
    replacements=(
        "REDIS_URL=$redisUrl"
        "REGISTRY_URL=$registry_url"
        "MONGO_USERNAME=$mongo_initdb_root_username"
        "MONGO_PASSWORD=$mongo_initdb_root_password"
        "MONGO_DB_NAME=$mongo_initdb_database"
        "MONOG_URL=$mongoUrl"
        "RABBITMQ_USERNAME=$rabbitmq_default_user"
        "RABBITMQ_PASSWORD=$rabbitmq_default_pass"
        "RABBITMQ_URL=$rabbitmqUrl"
        "PRIVATE_KEY=$private_key"
        "PUBLIC_KEY=$public_key"
        "BPP_SUBSCRIBER_URL=$bpp_subscriber_url"
        "BPP_SUBSCRIBER_ID=$bpp_subscriber_id"
        "BPP_SUBSCRIBER_KEY_ID=$bpp_subscriber_key_id"
        "WEBHOOK_URL=$webhook_url"
        "USE_LAYER_2_CONFIG"=true
        "MANDATE_LAYER_2_CONFIG"=true

    )

    echo "Configuring BPP protocol server"
    # Apply replacements in both files
    for file in "$clientFile" "$networkFile"; do
        for line in "${replacements[@]}"; do
            key=$(echo "$line" | cut -d '=' -f1)
            value=$(echo "$line" | cut -d '=' -f2)
            sed -i '' "s|$key|$value|" "$file"
        done

    done

else
    declare -A replacements=(
        ["REDIS_URL"]=$redisUrl
        ["REGISTRY_URL"]=$registry_url
        ["MONGO_USERNAME"]=$mongo_initdb_root_username
        ["MONGO_PASSWORD"]=$mongo_initdb_root_password
        ["MONGO_DB_NAME"]=$mongo_initdb_database
        ["MONOG_URL"]=$mongoUrl
        ["RABBITMQ_USERNAME"]=$rabbitmq_default_user
        ["RABBITMQ_PASSWORD"]=$rabbitmq_default_pass
        ["RABBITMQ_URL"]=$rabbitmqUrl
        ["PRIVATE_KEY"]=$private_key
        ["PUBLIC_KEY"]=$public_key
        ["BPP_SUBSCRIBER_URL"]=$bpp_subscriber_url
        ["BPP_SUBSCRIBER_ID"]=$bpp_subscriber_id
        ["BPP_SUBSCRIBER_KEY_ID"]=$bpp_subscriber_key_id
        ["WEBHOOK_URL"]=$webhook_url
        ["USE_LAYER_2_CONFIG"]=true
        ["MANDATE_LAYER_2_CONFIG"]=true        

    )

    echo "Configuring BPP protocol server"
    # Apply replacements in both files
    for file in "$clientFile" "$networkFile"; do
        for key in "${!replacements[@]}"; do
            sed -i "s|$key|${replacements[$key]}|" "$file"
        done
    done
fi

echo "Registering BPP protocol server on the registry"

create_network_participant "$registry_url" "application/json" "$bpp_subscriber_id" "$bpp_subscriber_key_id" "$bpp_subscriber_url" "$public_key" "$public_key" "$valid_from" "$valid_until" "$type"