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
    docker compose -f $1 up -d $2
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
    docker volume create registry_data_volume
    docker volume create registry_database_volume
    docker run --rm -v $SCRIPT_DIR/../registry_data/config:/source -v registry_data_volume:/target busybox cp /source/{envvars,logger.properties,swf.properties} /target/
    docker rmi busybox
}
# Function to start the MongoDB, Redis, and RabbitMQ Services
start_support_services(){
    #ignore orphaned containers warning
    export COMPOSE_IGNORE_ORPHANS=1
    echo "${GREEN}................Installing MongoDB................${NC}"
    docker compose -f docker-compose-app.yml up -d mongo_db
    echo "MongoDB installation successful"

    echo "${GREEN}................Installing RabbitMQ................${NC}"
    docker compose -f docker-compose-app.yml up -d queue_service
    echo "RabbitMQ installation successful"

    echo "${GREEN}................Installing Redis................${NC}"
    docker compose -f docker-compose-app.yml up -d redis_db
    echo "Redis installation successful"
}

install_gateway() {
    if [[ $1 && $2 ]]; then
        bash scripts/update_gateway_details.sh $1 $2
    else
        bash scripts/update_gateway_details.sh http://registry:3030
    fi
    echo "${GREEN}................Installing Gateway service................${NC}"
    start_container $gateway_docker_compose_file gateway
    echo "Registering Gateway in the registry"

    sleep 10
    # if [[ $1 && $2 ]]; then
    #     bash scripts/register_gateway.sh $2
    # else
    #     bash scripts/register_gateway.sh
    # fi
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
    start_container $registry_docker_compose_file registry
    sleep 10
    echo "Registry installation successful"
}

# Function to install BAP Protocol Server
install_bap_protocol_server(){
    start_support_services
    if [[ $1 ]];then
        registry_url=$1
        bap_subscriber_id=$2
        bap_subscriber_key_id=$3
        bap_subscriber_url=$4
        bash scripts/update_bap_config.sh $registry_url $bap_subscriber_id $bap_subscriber_key_id $bap_subscriber_url
    else
        bash scripts/update_bap_config.sh
    fi
    sleep 10
    docker volume create bap_client_config_volume
    docker volume create bap_network_config_volume
    docker run --rm -v $SCRIPT_DIR/../protocol-server-data:/source -v bap_client_config_volume:/target busybox cp /source/bap-client.yml /target/default.yml
    docker run --rm -v $SCRIPT_DIR/../protocol-server-data:/source -v bap_client_config_volume:/target busybox cp /source/bap-client.yaml-sample /target
    docker run --rm -v $SCRIPT_DIR/../protocol-server-data:/source -v bap_network_config_volume:/target busybox cp /source/bap-network.yml /target/default.yml
    docker run --rm -v $SCRIPT_DIR/../protocol-server-data:/source -v bap_network_config_volume:/target busybox cp /source/bap-network.yaml-sample /target
    docker rmi busybox

    start_container $bap_docker_compose_file "bap-client"
    start_container $bap_docker_compose_file "bap-network"
    sleep 10
    echo "Protocol server BAP installation successful"
}


# Function to install BPP Protocol Server without Sandbox
install_bpp_protocol_server(){
    start_support_services
    echo "${GREEN}................Installing Protocol Server for BPP................${NC}"
    
    if [[ $1 ]];then
        registry_url=$1
        bpp_subscriber_id=$2
        bpp_subscriber_key_id=$3
        bpp_subscriber_url=$4
        webhook_url=$5
        bash scripts/update_bpp_config.sh $registry_url $bpp_subscriber_id $bpp_subscriber_key_id $bpp_subscriber_url $webhook_url
    else
        bash scripts/update_bpp_config.sh
    fi

    sleep 10
    docker volume create bpp_client_config_volume
    docker volume create bpp_network_config_volume
    docker run --rm -v $SCRIPT_DIR/../protocol-server-data:/source -v bpp_client_config_volume:/target busybox cp /source/bpp-client.yml /target/default.yml
    docker run --rm -v $SCRIPT_DIR/../protocol-server-data:/source -v bpp_client_config_volume:/target busybox cp /source/bpp-client.yaml-sample /target
    docker run --rm -v $SCRIPT_DIR/../protocol-server-data:/source -v bpp_network_config_volume:/target busybox cp /source/bpp-network.yml /target/default.yml
    docker run --rm -v $SCRIPT_DIR/../protocol-server-data:/source -v bpp_network_config_volume:/target busybox cp /source/bpp-network.yaml-sample /target
    docker rmi busybox

    start_container $bpp_docker_compose_file "bpp-client"
    start_container $bpp_docker_compose_file "bpp-network"
    sleep 10
    echo "Protocol server BPP installation successful"
}

#Function to restart
restart_script(){
    read -p "${GREEN}Do you want to restart the script or exit the script? (r for restart, e for exit): ${NC}" choice
    if [[ $choice == "r" ]]; then
        echo "Restarting the script..."
        exec "$0"  # Restart the script by re-executing it
    elif [[ $choice == "e" ]]; then
        echo "Exiting the script..."
        exit 0
    fi
}

mergingNetworks(){
    echo -e "1. Merge Two Different Registries \n2. Merge Multiple Registries into a Super Registry"
    read -p "Enter your choice: " merging_network
    urls=()
    if [ "$merging_network" = "2" ]; then
        echo "${GREEN}Currently this feature is not available in this distribution of Beckn ONIX${NC}"
        restart_script
        while true; do
            read -p "Enter registry URL (or 'N' to stop): " url
            if [[ $url == 'N' ]]; then
                break
            else
                urls+=("$url")
            fi
        done
        read -p "Enter the Super Registry URL: " registry_super_url
    else
        echo "${GREEN}Currently this feature is not available in this distribution of Beckn ONIX${NC}"
        restart_script
        # read -p "Enter A registry URL: " registry_a_url
        # read -p "Enter B registry URL: " registry_b_url
        # urls+=("$registry_a_url")
    
    fi
    # Commenting below lines of code still we are activly working on it
    # if [[ ${#urls[@]} -gt 0 ]]; then
    #     echo "Entered registry URLs:"
    #     all_responses=""
    #     for url in "${urls[@]}"; do
    #         response=$(curl -s -H 'ACCEPT: application/json' -H 'CONTENT-TYPE: application/json' "$url"+/subscribers/lookup -d '{}')
    #         all_responses+="$response"
    #     done
    #     for element in $(echo "$all_responses" | jq -c '.[]'); do
    #         if [ "$merging_network" -eq 1 ]; then
    #             curl --location "$registry_b_url"+/subscribers/register \
    #                 --header 'Content-Type: application/json' \
    #                 --data "$element"
    #             echo
    #         else
    #             curl --location "$registry_super_url"+/subscribers/register \
    #                 --header 'Content-Type: application/json' \
    #                 --data "$element"
    #             echo
    #         fi
    #     done
    #     echo "Merging Multiple Registries into a Super Registry Done ..."
    # else
    #     echo "No registry URLs entered."
    # fi
    
    # if [ "$merging_network" = "2" ]; then
    #     echo "Merging Multiple Registries into a Super Registry"
    # else
    #     echo "Invalid option. Please restart the script and select a valid option."
    #     exit 1
    # fi
}



# Function to install BPP Protocol Server with Sandbox
install_bpp_protocol_server_with_sandbox(){
    start_support_services

    docker volume create bpp_client_config_volume
    docker volume create bpp_network_config_volume
    
    echo "${GREEN}................Installing Sandbox................${NC}"
    start_container $bpp_docker_compose_file_sandbox "sandbox-api"
    sleep 5
    echo "Sandbox installation successful"

    echo "${GREEN}................Installing Protocol Server for BPP................${NC}"
    
    if [[ $1 ]];then
        registry_url=$1
        bpp_subscriber_id=$2
        bpp_subscriber_key_id=$3
        bpp_subscriber_url=$4
        webhook_url=$5
        bash scripts/update_bpp_config.sh $registry_url $bpp_subscriber_id $bpp_subscriber_key_id $bpp_subscriber_url $webhook_url
    else
        bash scripts/update_bpp_config.sh
    fi

    sleep 10
    docker run --rm -v $SCRIPT_DIR/../protocol-server-data:/source -v bpp_client_config_volume:/target busybox cp /source/bpp-client.yml /target/default.yml
    docker run --rm -v $SCRIPT_DIR/../protocol-server-data:/source -v bpp_client_config_volume:/target busybox cp /source/bpp-client.yaml-sample /target
    docker run --rm -v $SCRIPT_DIR/../protocol-server-data:/source -v bpp_network_config_volume:/target busybox cp /source/bpp-network.yml /target/default.yml
    docker run --rm -v $SCRIPT_DIR/../protocol-server-data:/source -v bpp_network_config_volume:/target busybox cp /source/bpp-network.yaml-sample /target
    docker rmi busybox

    start_container $bpp_docker_compose_file "bpp-client"
    start_container $bpp_docker_compose_file "bpp-network"
    sleep 10
    echo "Protocol server BPP installation successful"
}


# Function to handle the setup process for each platform
completeSetup() {
    platform=$1

    public_address="https://<your public IP address>"

    echo "Proceeding with the setup for $platform..."
    
    # Insert the specific commands for each platform, including requesting network config if necessary
    case $platform in
        "Registry")
            read -p "Enter publicly accessible registry URL: " registry_url
            if [[ $registry_url =~ /$ ]]; then
                new_registry_url=${registry_url%/}
            else
                new_registry_url=$registry_url
            fi
            public_address=$registry_url
            install_package
            install_registry $new_registry_url
            ;;
        "Gateway"|"Beckn Gateway")
            read -p "Enter your registry URL: " registry_url
            read -p "Enter publicly accessible gateway URL: " gateway_url
            
            if [[ $registry_url =~ /$ ]]; then
                new_registry_url=${registry_url%/}
            else
                new_registry_url=$registry_url
            fi
            if [[ $gateway_url =~ /$ ]]; then
                gateway_url=${gateway_url%/}
            fi

            public_address=$gateway_url
            install_package
            install_gateway $new_registry_url $gateway_url
            ;;
        "BAP")
            echo "${GREEN}................Installing Protocol Server for BAP................${NC}"
            
            read -p "Enter BAP Subscriber ID: " bap_subscriber_id
            read -p "Enter BAP Subscriber URL: " bap_subscriber_url
            read -p "Enter the registry_url(e.g. https://registry.becknprotocol.io/subscribers): " registry_url
            bap_subscriber_key_id=$bap_subscriber_id-key
            public_address=$bap_subscriber_url
            install_package
            install_bap_protocol_server $registry_url $bap_subscriber_id $bap_subscriber_key_id $bap_subscriber_url
            ;;
        "BPP")
            echo "${GREEN}................Installing Protocol Server for BAP................${NC}"
            read -p "Enter BPP Subscriber ID: " bpp_subscriber_id
            read -p "Enter BPP Subscriber URL: " bpp_subscriber_url
            read -p "Enter the registry_url(e.g. https://registry.becknprotocol.io/subscribers): " registry_url
            read -p "Enter Webhook URL: " webhook_url

            bpp_subscriber_key_id=$bpp_subscriber_id-key
            public_address=$bpp_subscriber_url
            install_package
            install_bpp_protocol_server $registry_url $bpp_subscriber_id $bpp_subscriber_key_id $bpp_subscriber_url $webhook_url
            ;;
        *)
            echo "Invalid platform selected."
            exit 1
            ;;
    esac

    echo "[Installation Logs]"
    echo -e "${boldGreen}Your $platform setup is complete.${reset}"
    echo -e "${boldGreen}You can access your $platform at $public_address ${reset}"
    # Key generation and subscription logic follows here
}


# MAIN SCRIPT STARTS HERE

echo "Welcome to Beckn-ONIX!"
if [ -f ./onix_ascii_art.txt ]; then
    cat ./onix_ascii_art.txt
else
    echo "[Display Beckn-ONIX ASCII Art]"
fi

echo "Beckn-ONIX is a platform that helps you quickly launch and configure beckn-enabled networks."
echo -e "\nWhat would you like to do?\n1. Join an existing network\n2. Create new production network\n3. Set up a network on your local machine\n4. Merge multiple networks\n5. Configure Existing Network\n(Press Ctrl+C to exit)"
read -p "Enter your choice: " choice

boldGreen="\e[1m\e[92m"
reset="\e[0m"
if [[ $choice -eq 3 ]]; then
    echo "Installing all components on the local machine"
    install_package
    install_registry
    install_gateway
    install_bap_protocol_server
    install_bpp_protocol_server_with_sandbox
elif [[ $choice -eq 4 ]]; then
    echo "Determining the platforms available based on the initial choice"
    mergingNetworks
else
    # Determine the platforms available based on the initial choice
    platforms=("Gateway" "BAP" "BPP")
    [ "$choice" -eq 2 ] && platforms=("Registry" "${platforms[@]}")  # Add Registry for new network setups

    echo "Great choice! Get ready."
    echo -e "\nWhich platform would you like to set up?"
    for i in "${!platforms[@]}"; do 
        echo "$((i+1)). ${platforms[$i]}"
    done

    read -p "Enter your choice: " platform_choice

    selected_platform="${platforms[$((platform_choice-1))]}"

    if [[ -n $selected_platform ]]; then
        completeSetup "$selected_platform"
    else
        echo "Invalid option. Please restart the script and select a valid option."
        exit 1
    fi
fi

echo "Process complete. Thank you for using Beckn-ONIX!"
