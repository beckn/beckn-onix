#!/bin/bash
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
source $SCRIPT_DIR/get_container_details.sh

gateway_url=gateway
gateway_port=4030
protocol=http
reg_url=http://$1:3030/subscribers/lookup

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
        # Print the extracted keys
        echo "Signing Public Key: $signing_public_key"
        echo "Encryption Public Key: $encr_public_key"
        echo "URL $subscriber_url"

        cp $SCRIPT_DIR/../gateway_data/config/swf.properties-sample $SCRIPT_DIR/../gateway_data/config/swf.properties
        config_file="$SCRIPT_DIR/../gateway_data/config/swf.properties"
        
        tmp_file=$(mktemp "tempfile.XXXXXXXXXX")
        sed " s|SUBSCRIBER_ID|$gateway_url|g; s|SIGNING_PUBLIC_KEY|$signing_public_key|g; s|ENCRYPTION_PUBLIC_KEY|$encr_public_key|g; s|GATEWAY_URL|$gateway_url|g; s|GATEWAY_PORT|$gateway_port|g; s|PROTOCOL|$protocol|g; s|REGISTRY_URL|$subscriber_url|g" "$config_file" > "$tmp_file"
        mv "$tmp_file" "$config_file"
}

if [[ $1 == https://* ]]; then
 reg_url=$1/subscribers/lookup
 get_details_registry $reg_url
else
    service_name=$1
    if [[ $(uname -s) == 'Darwin' ]]; then
        ip=localhost
    elif [[ $(systemd-detect-virt) == 'wsl' ]]; then
        ip=$(hostname -I | awk '{print $1}')
    else
        ip=$(get_container_ip $service_name)
    fi
    reg_url=http://$ip:3030/subscribers/lookup
    get_details_registry $reg_url
fi


if [[ $2 ]]; then
    if [[ $2 == https://* ]]; then
        if [[ $(uname -s) == 'Darwin' ]]; then
            gateway_url=$(echo "$2" | sed -E 's/https:\/\///')
        else
            gateway_url=$(echo "$2" | sed 's/https:\/\///')
        fi
        gateway_port=443
        protocol=https
        update_gateway_config
    elif [[ $2 == http://* ]]; then
        if [[ $(uname -s) == 'Darwin' ]]; then
            gateway_url=$(echo "$2" | sed -E 's/http:\/\///')
        else
            gateway_url=$(echo "$2" | sed 's/http:\/\///')
        fi
        gateway_port=80
        protocol=http
        update_gateway_config
    fi
else
    update_gateway_config
fi