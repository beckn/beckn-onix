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
        bap_subscriber_key_id=$3
        bap_subscriber_url=$4
        bash scripts/update_bap_config.sh $registry_url $bap_subscriber_id $bap_subscriber_key_id $bap_subscriber_url
    else
        bash scripts/update_bap_config.sh
    fi
    sleep 10
    start_container "bap-client"
    start_container "bap-network"
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
    start_container "bpp-client"
    start_container "bpp-network"
    sleep 10
    echo "Protocol server BPP installation successful"
}



# MAIN SCRIPT STARTS HERE
#!/bin/bash

echo "Welcome to Beckn-ONIX!"
if [ -f ./onix_ascii_art.txt ]; then
    cat ./onix_ascii_art.txt
else
    echo "[Display Beckn-ONIX ASCII Art]"
fi

echo "Beckn ONIX is a platform that helps you quickly launch and configure beckn-enabled networks."
echo -e "\nWhat would you like to do?\n1. Join an existing network\n2. Create new production network\n3. Set up a network on your local machine\n4. Merge multiple networks\n5. Configure Existing Network\n(Press Ctrl+C to exit)"
read -p "Enter your choice: " choice

boldGreen="\e[1m\e[92m"
reset="\e[0m"

# Function to request network configuration URL
requestNetworkConfig() {
    echo "Please provide the network-specific configuration URL."
    read -p "Paste the URL of the network configuration here (or press Enter to skip): " config_url
    if [ -n "$config_url" ]; then
        echo "Network configuration URL provided: $config_url"
    else
        echo "No network configuration URL provided, proceeding without it."
    fi
    echo ""
}

# Function to handle the setup process for each platform
completeSetup() {
    platform=$1
    config_url=$2  # Passing this as an argument, though it could be optional or ignored by some setups

    public_address="https://<your public IP address>"

    echo "Proceeding with the setup for $platform..."
    if [ -n "$config_url" ]; then
        echo "Using network configuration from: $config_url"
    fi
    
    # Insert the specific commands for each platform, including requesting network config if necessary
    case $platform in
        "Registry")
            requestNetworkConfig
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
            requestNetworkConfig
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
            requestNetworkConfig
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
            requestNetworkConfig
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
    # Note: Passing an empty string for config_url since it's optionally handled within `completeSetup`
    completeSetup "$selected_platform" ""
else
    echo "Invalid option. Please restart the script and select a valid option."
    exit 1
fi

echo "Process complete. Thank you for using Beckn-ONIX!"


