#!/bin/bash
source scripts/variables.sh
source scripts/get_container_details.sh

# Function to start a specific service inside docker-compose file
install_package(){
    echo "${GREEN}................Installing required packages................${NC}"
    bash scripts/package_manager.sh
    echo "Package Installation is done"

}
start_container(){
    #ignore orphaned containers warning
    export COMPOSE_IGNORE_ORPHANS=1
    docker-compose -f docker-compose-v2.yml up -d $1
}

update_registry_details() {
    if [[ $1 ]];then
        if [[ $1 == https://* ]]; then
            if [[ $(uname -s) == 'Darwin' ]]; then
                registry_url=$(echo "$1" | sed -E 's/https:\/\///')
            else
                registry_url=$(echo "$1" | sed 's/https:\/\///')
            fi
            registry_port=443
            protocol=https
        elif [[ $1 == http://* ]]; then
            if [[ $(uname -s) == 'Darwin' ]]; then
                registry_url=$(echo "$1" | sed -E 's/http:\/\///')
            else
                registry_url=$(echo "$1" | sed 's/http:\/\///')
            fi
            registry_port=80
            protocol=http
        fi

    else
        registry_url=registry  
        registry_port=3030
        protocol=http
    fi
    echo $registry_url
    cp $SCRIPT_DIR/../registry_data/config/swf.properties-sample $SCRIPT_DIR/../registry_data/config/swf.properties
    config_file="$SCRIPT_DIR/../registry_data/config/swf.properties"
        
    tmp_file=$(mktemp "tempfile.XXXXXXXXXX")
    sed "s|REGISTRY_URL|$registry_url|g; s|REGISTRY_PORT|$registry_port|g; s|PROTOCOL|$protocol|g" "$config_file" > "$tmp_file"
    mv "$tmp_file" "$config_file"

}
# Function to start the MongoDB, Redis, and RabbitMQ Services
start_support_services(){
    #ignore orphaned containers warning
    export COMPOSE_IGNORE_ORPHANS=1
    echo "${GREEN}................Installing MongoDB................${NC}"
    docker-compose -f docker-compose-app.yml up -d mongo_db
    echo "MongoDB installation successful"

    echo "${GREEN}................Installing RabbitMQ................${NC}"
    docker-compose -f docker-compose-app.yml up -d queue_service
    echo "RabbitMQ installation successful"

    echo "${GREEN}................Installing Redis................${NC}"
    docker-compose -f docker-compose-app.yml up -d redis_db
    echo "Redis installation successful"
}

install_gateway() {
    if [[ $1 && $2 ]]; then
        bash scripts/update_gateway_details.sh $1 $2
    else
        bash scripts/update_gateway_details.sh registry 
    fi
    echo "${GREEN}................Installing Gateway service................${NC}"
    start_container gateway
    echo "Registering Gateway in the registry"

    sleep 10
    if [[ $1 && $2 ]]; then
        bash scripts/register_gateway.sh $2
    else
        bash scripts/register_gateway.sh
    fi
    echo " "
    echo "Gateway installation successful"
}

# Function to install Beckn Gateway and Beckn Registry
install_registry(){
    if [[ $1 ]]; then
        update_registry_details $1
    else
        update_registry_details
    fi

    echo "${GREEN}................Installing Registry service................${NC}"
    start_container registry
    sleep 10
    echo "Registry installation successful"
}

# Function to install BAP Protocol Server
install_bap_protocol_server(){
    start_support_services
    if [[ $1 ]];then
        registry_url=$1
        bap_subscriber_id=$2
        bap_subscriber_id_key=$3
        bap_subscriber_url=$4
        bash scripts/update_bap_config.sh $registry_url $bap_subscriber_id $bap_subscriber_id_key $bap_subscriber_url
    else
        bash scripts/update_bap_config.sh
    fi
    sleep 10
    start_container "bap-client"
    start_container "bap-network"
    sleep 10
    echo "Protocol server BAP installation successful"
}

# Function to install BPP Protocol Server with BPP Sandbox
install_bpp_protocol_server_with_sandbox(){
    start_support_services
    echo "${GREEN}................Installing Sandbox................${NC}"
    start_container "sandbox-api"
    sleep 5
    echo "Sandbox installation successful"

    echo "${GREEN}................Installing Webhook................${NC}"
    start_container "sandbox-webhook"
    sleep
    echo "Webhook installation successful"

    echo "${GREEN}................Installing Protocol Server for BPP................${NC}"

    if [[ $1 ]];then
        registry_url=$1
        bpp_subscriber_id=$2
        bpp_subscriber_id_key=$3
        bpp_subscriber_url=$4
        bash scripts/update_bpp_config.sh $registry_url $bpp_subscriber_id $bpp_subscriber_id_key $bpp_subscriber_url
    else
        bash scripts/update_bpp_config.sh
    fi

    sleep 10
    start_container "bpp-client"
    start_container "bpp-network"
    sleep 10
    echo "Protocol server BPP installation successful"
}

# Function to install BPP Protocol Server without Sandbox
install_bpp_protocol_server(){
    start_support_services
    echo "${GREEN}................Installing Protocol Server for BPP................${NC}"
    
    if [[ $1 ]];then
        registry_url=$1
        bpp_subscriber_id=$2
        bpp_subscriber_id_key=$3
        bpp_subscriber_url=$4
        webhook_url=$5
        bash scripts/update_bpp_config.sh $registry_url $bpp_subscriber_id $bpp_subscriber_id_key $bpp_subscriber_url $$webhook_url
    else
        bash scripts/update_bpp_config.sh
    fi

    sleep 10
    start_container "bpp-client"
    start_container "bpp-network"
    sleep 10
    echo "Protocol server BPP installation successful"
}

text="
The following components will be installed

1. Registry
2. Gateway
3. Sandbox
4. Sandbox Webhook
5. Protocol Server for BAP
6. Protocol Server for BPP
"

# Main script starts here
bash scripts/banner.sh
echo "Welcome to ONIX"
echo "$text"

read -p "${GREEN}Do you want to install all the components on the local system? (Y/n): ${NC}" install_all

if [[ $install_all =~ ^[Yy]$ ]]; then
    # Install and bring up everything
    install_package
    install_registry
    install_gateway
    start_support_services
    install_bap_protocol_server
    install_bpp_protocol_server_with_sandbox
else
    # User selects specific components to install
    echo "Please select the components that you want to install"
    echo "1. Beckn Gateway & Beckn Registry"
    echo "2. BAP Protocol Server"
    echo "3. BPP Protocol Server with BPP Sandbox"
    echo "4. BPP Protocol Server"
    echo "5. Generic Client Layer"
    echo "6. Exit"
    
    read -p "Enter your choice (1-6): " user_choice

    case $user_choice in
        1)
            echo "${GREEN}Default Registry URL: $registry_url"
            echo "Default Gateway URL will be docker URL"
            read -p "Do you want to change Registry and Gateway URL? (Y/N): ${NC}" change_url
            if [[ $change_url =~ ^[Yy]$ ]]; then
                read -p "Enter publicly accessible registry URL: " registry_url
                read -p "Enter publicly accessible gateway URL: " gateway_url
                
                if [[ $registry_url =~ /$ ]]; then
                    new_registry_url=${registry_url%/}
                else
                    new_registry_url=$registry_url
                fi
                if [[ $gateway_url =~ /$ ]]; then
                    gateway_url=${gateway_url%/}
                fi

                install_package
                install_registry $new_registry_url
                install_gateway $new_registry_url $gateway_url

            else
                install_package
                install_registry
                install_gateway
            fi
            ;;
        2)
            echo "${GREEN}................Installing Protocol Server for BAP................${NC}"
            
            read -p "Enter BAP Subscriber ID: " bap_subscriber_id
            read -p "Enter BAP Subscriber URL: " bap_subscriber_url
            # Ask the user if they want to change the registry_url
            read -p "Do you want to change the registry_url? (${GREEN}Press Enter to accept default: $beckn_registry_url${NC}): " custom_registry_url
            registry_url=${custom_registry_url:-$beckn_registry_url}
            bap_subscriber_id_key=$bap_subscriber_id-key
            install_package
            install_bap_protocol_server $registry_url $bap_subscriber_id $bap_subscriber_id_key $bap_subscriber_url
            ;;
        3)
            read -p "Enter BPP Subscriber ID: " bpp_subscriber_id
            read -p "Enter BPP Subscriber URL: " bpp_subscriber_url
            # Ask the user if they want to change the registry_url
            read -p "Do you want to change the registry_url? (${GREEN}Press Enter to accept default: $beckn_registry_url${NC}): " custom_registry_url
            registry_url=${custom_registry_url:-$beckn_registry_url}
            bpp_subscriber_id_key=$bpp_subscriber_id-key
            install_package
            install_bpp_protocol_server_with_sandbox $registry_url $bpp_subscriber_id $bpp_subscriber_id_key $bpp_subscriber_url
            ;;
        4)
            read -p "Enter BPP Subscriber ID: " bpp_subscriber_id
            read -p "Enter BPP Subscriber URL: " bpp_subscriber_url
            read -p "Enter Webhook URL: " webhook_url
            
            # Ask the user if they want to change the registry_url
            read -p "Do you want to change the registry_url? (${GREEN}Press Enter to accept default: $beckn_registry_url${NC}): " custom_registry_url
            registry_url=${custom_registry_url:-$beckn_registry_url}
            bpp_subscriber_id_key=$bpp_subscriber_id-key
            install_package
            install_bpp_protocol_server $registry_url $bpp_subscriber_id $bpp_subscriber_id_key $bpp_subscriber_url $webhook_url
            ;;

        5)
            echo "${GREEN}................Installing GENERIC CLIENT LAYER................${NC}"
            read -p "Enter BAP Subscriber ID: " bap_subscriber_id
            read -p "Enter BAP Subscriber URL: " bap_subscriber_url
            read -p "Enter BAP Client URL: " bap_client_url
            bash scripts/generic-client-layer.sh $bap_subscriber_id $bap_subscriber_url $bap_client_url
            start_container "generic-client-layer"
            ;;

        6)
            echo "Exiting ONIX"
            exit 0
            ;;
        *)
            echo "Invalid choice. Exiting ONIX."
            exit 1
            ;;
    esac
fi
