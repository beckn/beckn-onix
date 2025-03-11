#!/bin/bash
source scripts/variables.sh
source scripts/get_container_details.sh

# Function to start a specific service inside docker-compose file
install_package() {
    echo "${GREEN}................Installing required packages................${NC}"
    bash scripts/package_manager.sh
    echo "Package Installation is done"

}
start_container() {
    #ignore orphaned containers warning
    export COMPOSE_IGNORE_ORPHANS=1
    docker compose -f $1 up -d $2
}

update_registry_details() {
    if [[ $1 ]]; then
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
    sed "s|REGISTRY_URL|$registry_url|g; s|REGISTRY_PORT|$registry_port|g; s|PROTOCOL|$protocol|g" "$config_file" >"$tmp_file"
    mv "$tmp_file" "$config_file"
    docker volume create registry_data_volume
    docker volume create registry_database_volume
    docker run --rm -v $SCRIPT_DIR/../registry_data/config:/source -v registry_data_volume:/target busybox cp /source/{envvars,logger.properties,swf.properties} /target/
    docker rmi busybox
}
# Function to start the MongoDB, Redis, and RabbitMQ Services
start_support_services() {
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
install_registry() {
    if [[ $1 ]]; then
        update_registry_details $1
    else
        update_registry_details
    fi

    echo "${GREEN}................Installing Registry service................${NC}"
    start_container $registry_docker_compose_file registry
    sleep 10
    echo "Registry installation successful"

    #Update Role Permission for registry.
    if [[ $1 ]]; then
        bash scripts/registry_role_permissions.sh $1
    else
        bash scripts/registry_role_permissions.sh 
    fi     
}

# Function to install Layer2 Config
install_layer2_config() {
    container_name=$1
    FILENAME="$(basename "$layer2_url")"
    wget -O "$(basename "$layer2_url")" "$layer2_url" >/dev/null 2>&1
    if [ $? -eq 0 ]; then
        docker cp "$FILENAME" $container_name:"$schemas_path/$FILENAME" >/dev/null 2>&1
        if [ $? -eq 0 ]; then
            echo "${GREEN}Successfully copied $FILENAME to Docker container $container_name.${NC}"
        fi
    else
        echo "${BoldRed}The Layer 2 configuration file has not been downloaded.${NC}"
        echo -e "${BoldGreen}Please download the Layer 2 configuration files by running the download_layer_2_config_bap.sh script located in the ../layer2 folder."
        echo -e "For further information, refer to this URL: https://github.com/beckn/beckn-onix/blob/main/docs/user_guide.md#downloading-layer-2-configuration-for-a-domain.${NC}"
    fi
    rm -f $FILENAME >/dev/null 2>&1
}

# Function to install BAP Protocol Server
install_bap_protocol_server() {
    start_support_services
    if [[ $1 ]]; then
        registry_url=$1
        bap_subscriber_id=$2
        bap_subscriber_key_id=$3
        bap_subscriber_url=$4
        bash scripts/update_bap_config.sh $registry_url $bap_subscriber_id $bap_subscriber_key_id $bap_subscriber_url $api_key $np_domain
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

    if [[ -z "$layer2_url" ]]; then
        echo -e "${BoldGreen}Please download the Layer 2 configuration files by running the download_layer_2_config_bap.sh script located in the ../layer2 folder."
        echo -e "For further information, refer to this URL:${BLUE}https://github.com/beckn/beckn-onix/blob/main/docs/user_guide.md#downloading-layer-2-configuration-for-a-domain.${NC}"
    else
        echo -e "${GREEN}Installing layer configuration for $(basename "$layer2_url")${NC}"
        install_layer2_config bap-client
        install_layer2_config bap-network
    fi
    echo "Protocol server BAP installation successful"
}

# Function to install BPP Protocol Server without Sandbox
install_bpp_protocol_server() {
    start_support_services
    echo "${GREEN}................Installing Protocol Server for BPP................${NC}"

    if [[ $1 ]]; then
        registry_url=$1
        bpp_subscriber_id=$2
        bpp_subscriber_key_id=$3
        bpp_subscriber_url=$4
        webhook_url=$5
        bash scripts/update_bpp_config.sh $registry_url $bpp_subscriber_id $bpp_subscriber_key_id $bpp_subscriber_url $webhook_url $api_key $np_domain
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
    if [[ -z "$layer2_url" ]]; then
        echo -e "${BoldGreen}Please download the Layer 2 configuration files by running the download_layer_2_config_bpp.sh script located in the ../layer2 folder."
        echo -e "For further information, refer to this URL:${BLUE} https://github.com/beckn/beckn-onix/blob/main/docs/user_guide.md#downloading-layer-2-configuration-for-a-domain.${NC}"
    else
        echo -e "${BoldGreen}Installing layer configuration for $(basename "$layer2_url")"
        install_layer2_config bpp-client
        install_layer2_config bpp-network
    fi
    echo "Protocol server BPP installation successful"
}

mergingNetworks() {
    echo -e "1. Merge Two Different Registries \n2. Merge Multiple Registries into a Super Registry"
    read -p "Enter your choice: " merging_network
    urls=()
    if [ "$merging_network" = "2" ]; then
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
        read -p "Enter A registry URL: " registry_a_url
        read -p "Enter B registry URL: " registry_b_url
        urls+=("$registry_a_url")

    fi
    if [[ ${#urls[@]} -gt 0 ]]; then
        echo "Entered registry URLs:"
        all_responses=""
        for url in "${urls[@]}"; do
            response=$(curl -s -H 'ACCEPT: application/json' -H 'CONTENT-TYPE: application/json' "$url"+/subscribers/lookup -d '{}')
            all_responses+="$response"
        done
        for element in $(echo "$all_responses" | jq -c '.[]'); do
            if [ "$merging_network" -eq 1 ]; then
                curl --location "$registry_b_url"+/subscribers/register \
                    --header 'Content-Type: application/json' \
                    --data "$element"
                echo
            else
                curl --location "$registry_super_url"+/subscribers/register \
                    --header 'Content-Type: application/json' \
                    --data "$element"
                echo
            fi
        done
        echo "Merging Multiple Registries into a Super Registry Done ..."
    else
        echo "No registry URLs entered."
    fi

    if [ "$merging_network" = "2" ]; then
        echo "Merging Multiple Registries into a Super Registry"
    else
        echo "Invalid option. Please restart the script and select a valid option."
        exit 1
    fi
}

# Function to install BPP Protocol Server with Sandbox
install_bpp_protocol_server_with_sandbox() {
    start_support_services

    docker volume create bpp_client_config_volume
    docker volume create bpp_network_config_volume

    echo "${GREEN}................Installing Sandbox................${NC}"
    start_container $bpp_docker_compose_file_sandbox "sandbox-api"
    sleep 5
    echo "Sandbox installation successful"

    echo "${GREEN}................Installing Protocol Server for BPP................${NC}"

    if [[ $1 ]]; then
        registry_url=$1
        bpp_subscriber_id=$2
        bpp_subscriber_key_id=$3
        bpp_subscriber_url=$4
        webhook_url=$5
        bash scripts/update_bpp_config.sh $registry_url $bpp_subscriber_id $bpp_subscriber_key_id $bpp_subscriber_url $webhook_url $api_key $np_domain
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

layer2_config() {
    while true; do
        read -p "Paste the URL of the Layer 2 configuration here (or press Enter to skip): " layer2_url
        if [[ -z "$layer2_url" ]]; then
            break #If URL is empty then skip the URL validation
        elif [[ $layer2_url =~ ^(http|https):// ]]; then
            break
        else
            echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
        fi
    done
}

# Validate the user credentials against the Registry
validate_user() {
    # Prompt for username
    read -p "Enter your registry username: " username

    # Prompt for password with '*' masking
    echo -n "Enter your registry password: "
    stty -echo # Disable terminal echo

    password=""
    while IFS= read -r -n1 char; do
        if [[ "$char" == $'\0' ]]; then
            break
        fi
        password+="$char"
        echo -n "*" # Display '*' for each character typed
    done
    stty echo # Re-enable terminal echo
    echo      # Move to a new line after input

    # Replace '/subscribers' with '/login' for validation
    local login_url="${registry_url%/subscribers}/login"

    # Validate credentials using a POST request
    local response
    response=$(curl -s -w "%{http_code}" -X POST "$login_url" \
        -H "Content-Type: application/json" \
        -d '{ "Name" : "'"$username"'", "Password" : "'"$password"'" }')

    # Check if the HTTP response is 200 (success)
    status_code="${response: -3}"
    if [ "$status_code" -eq 200 ]; then
        response_body="${response%???}"
        api_key=$(echo "$response_body" | jq -r '.api_key')
        return 0
    else
        echo "Please check your credentials or register new user on $login_url"
        return 1
    fi
}

get_np_domain() {
    if [[ $2 ]]; then
        read -p "Do you want to setup this $1 and $2 for specific domain? {Y/N} " dchoice
    else
        read -p "Do you want to setup this $1 for specific domain? {Y/N} " dchoice
    fi

    if [[ "$dchoice" == "Y" || "$dchoice" == "y" ]]; then
        local login_url="${registry_url%/subscribers}"
        read -p "Enter the domain name for $1 : " np_domain
        domain_present=$(curl -s -H "ApiKey:$api_key" --header 'Content-Type: application/json' $login_url/network_domains/index | jq -r '.[].name' | tr '\n' ' ')
        if echo "$domain_present" | grep -Fqw "$np_domain"; then
            return 0
        else
            echo "${BoldRed}The domain '$np_domain' is NOT present in the network domains.${NC}"
            echo "${BoldGreen}Available network domains: $domain_present ${NC}"
        fi
    else
        np_domain=" " #If user don't want to add specific domain then save empty string
        return 0
    fi
}

# Function to handle the setup process for each platform
completeSetup() {
    platform=$1

    public_address="https://<your public IP address>"

    echo "Proceeding with the setup for $platform..."

    case $platform in
    "Registry")
        while true; do
            read -p "Enter publicly accessible registry URL: " registry_url
            if [[ $registry_url =~ ^(http|https):// ]]; then
                break
            else
                echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
            fi
        done

        new_registry_url="${registry_url%/}"
        public_address=$registry_url
        install_package
        install_registry $new_registry_url
        ;;
    "Gateway" | "Beckn Gateway")
        while true; do
            read -p "Enter your registry URL: " registry_url
            if [[ $registry_url =~ ^(http|https):// ]]; then
                break
            else
                echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
            fi
        done

        while true; do
            read -p "Enter publicly accessible gateway URL: " gateway_url
            if [[ $gateway_url =~ ^(http|https):// ]]; then
                gateway_url="${gateway_url%/}"
                break
            else
                echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
            fi
        done

        public_address=$gateway_url
        install_package
        install_gateway $registry_url $gateway_url
        ;;
    "BAP")
        echo "${GREEN}................Installing Protocol Server for BAP................${NC}"

        read -p "Enter BAP Subscriber ID: " bap_subscriber_id
        while true; do
            read -p "Enter BAP Subscriber URL: " bap_subscriber_url
            if [[ $bap_subscriber_url =~ ^(http|https):// ]]; then
                break
            else
                echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
            fi
        done

        while true; do
            read -p "Enter the registry URL (e.g., https://registry.becknprotocol.io/subscribers): " registry_url
            if [[ $registry_url =~ ^(http|https):// ]] && [[ $registry_url == */subscribers ]]; then
                break
            else
                echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
            fi
        done
        validate_user
        if [ $? -eq 1 ]; then
            exit
        fi

        get_np_domain $bap_subscriber_id
        if [ $? -eq 1 ]; then
            exit
        fi

        bap_subscriber_key_id="$bap_subscriber_id-key"
        public_address=$bap_subscriber_url

        layer2_config
        install_package
        install_bap_protocol_server $registry_url $bap_subscriber_id $bap_subscriber_key_id $bap_subscriber_url
        ;;
    "BPP")
        echo "${GREEN}................Installing Protocol Server for BPP................${NC}"

        read -p "Enter BPP Subscriber ID: " bpp_subscriber_id
        while true; do
            read -p "Enter BPP Subscriber URL: " bpp_subscriber_url
            if [[ $bpp_subscriber_url =~ ^(http|https):// ]]; then
                break
            else
                echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
            fi
        done

        while true; do
            read -p "Enter Webhook URL: " webhook_url
            if [[ $webhook_url =~ ^(http|https):// ]]; then
                break
            else
                echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
            fi
        done

        while true; do
            read -p "Enter the registry URL (e.g., https://registry.becknprotocol.io/subscribers): " registry_url
            if [[ $registry_url =~ ^(http|https):// ]] && [[ $registry_url == */subscribers ]]; then
                break
            else
                echo "${RED}Please mention /subscribers in your registry URL${NC}"
            fi
        done
        validate_user
        if [ $? -eq 1 ]; then
            exit
        fi

        get_np_domain $bpp_subscriber_id
        if [ $? -eq 1 ]; then
            exit
        fi

        bpp_subscriber_key_id="$bpp_subscriber_id-key"
        public_address=$bpp_subscriber_url

        layer2_config
        install_package
        install_bpp_protocol_server $registry_url $bpp_subscriber_id $bpp_subscriber_key_id $bpp_subscriber_url $webhook_url
        ;;
    "ALL")
        # Collect all inputs at once for all components

        # Registry input
        while true; do
            read -p "Enter publicly accessible registry URL: " registry_url
            if [[ $registry_url =~ ^(http|https):// ]]; then
                break
            else
                echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
            fi
        done

        # Gateway inputs
        while true; do
            read -p "Enter publicly accessible gateway URL: " gateway_url
            if [[ $gateway_url =~ ^(http|https):// ]]; then
                gateway_url="${gateway_url%/}"
                break
            else
                echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
            fi
        done

        # BAP inputs
        read -p "Enter BAP Subscriber ID: " bap_subscriber_id
        while true; do
            read -p "Enter BAP Subscriber URL: " bap_subscriber_url
            if [[ $bap_subscriber_url =~ ^(http|https):// ]]; then
                break
            else
                echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
            fi
        done

        # BPP inputs
        read -p "Enter BPP Subscriber ID: " bpp_subscriber_id
        while true; do
            read -p "Enter BPP Subscriber URL: " bpp_subscriber_url
            if [[ $bpp_subscriber_url =~ ^(http|https):// ]]; then
                break
            else
                echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
            fi
        done

        while true; do
            read -p "Enter Webhook URL: " webhook_url
            if [[ $webhook_url =~ ^(http|https):// ]]; then
                break
            else
                echo "${RED}Invalid URL format. Please enter a valid URL starting with http:// or https://.${NC}"
            fi
        done

        # Install components after gathering all inputs
        install_package

        install_registry $registry_url

        install_gateway $registry_url $gateway_url

        layer2_config
        #Append /subscribers for registry_url
        new_registry_url="${registry_url%/}/subscribers"
        bap_subscriber_key_id="$bap_subscriber_id-key"
        install_bap_protocol_server $new_registry_url $bap_subscriber_id $bap_subscriber_key_id $bap_subscriber_url

        bpp_subscriber_key_id="$bpp_subscriber_id-key"
        install_bpp_protocol_server $new_registry_url $bpp_subscriber_id $bpp_subscriber_key_id $bpp_subscriber_url $webhook_url
        ;;
    *)
        echo "Unknown platform: $platform"
        ;;
    esac
}

restart_script() {
    read -p "${GREEN}Do you want to restart the script or exit the script? (r for restart, e for exit): ${NC}" choice
    if [[ $choice == "r" ]]; then
        echo "Restarting the script..."
        exec "$0" # Restart the script by re-executing it
    elif [[ $choice == "e" ]]; then
        echo "Exiting the script..."
        exit 0
    fi
}

# Function to validate user input
validate_input() {
    local input=$1
    local max_option=$2

    # Check if the input is a digit and within the valid range
    if [[ "$input" =~ ^[0-9]+$ ]] && ((input >= 1 && input <= max_option)); then
        return 0 # Valid input
    else
        echo "${RED}Invalid input. Please enter a number between 1 and $max_option.${NC}"
        return 1 # Invalid input
    fi
}

check_docker_permissions() {
    if ! command -v docker &>/dev/null; then
        echo -e "${RED}Error: Docker is not installed on this system.${NC}"
        if [[ "$OSTYPE" == "linux-gnu"* ]]; then
            install_package
            if [[ $? -ne 0 ]]; then
                echo -e "${RED}Please install Docker and try again.${NC}"
                echo -e "${RED}Please install Docker and jq manually.${NC}"
                exit 1
            fi
        fi
    fi
    if [[ "$OSTYPE" == "linux-gnu"* ]]; then
        if ! groups "$USER" | grep -q '\bdocker\b'; then
            echo -e "${RED}Error: You do not have permission to run Docker. Please add yourself to the docker group by running the following command:${NC}"
            echo -e "${BoldGreen}sudo usermod -aG docker \$USER"
            echo -e "After running the above command, please log out and log back in to your system, then restart the deployment script.${NC}"
            exit 1
        fi
    fi
}

# Function to update/upgrade a specific service
update_service() {
    service_name=$1
    docker_compose_file=$2
    image_name=$3

    echo "${GREEN}................Updating $service_name................${NC}"

    export COMPOSE_IGNORE_ORPHANS=1
    # Pull the latest image
    docker pull "$image_name"

    # Stop and remove the existing container
    docker compose -f "$docker_compose_file" stop "$service_name"
    docker compose -f "$docker_compose_file" rm -f "$service_name"

    # Start the service with the new image
    docker compose -f "$docker_compose_file" up -d "$service_name"

    echo "$service_name update successful"
}

# Function to handle the update/upgrade process
update_network() {
    echo -e "\nWhich component would you like to update?\n1. Registry\n2. Gateway\n3. BAP Protocol Server\n4. BPP Protocol Server\n5. All components"
    read -p "Enter your choice: " update_choice

    validate_input "$update_choice" 5
    if [[ $? -ne 0 ]]; then
        restart_script
    fi

    case $update_choice in
    1)
        update_service "registry" "$registry_docker_compose_file" "fidedocker/registry"
        ;;
    2)
        update_service "gateway" "$gateway_docker_compose_file" "fidedocker/gateway"
        ;;
    3)
        update_service "bap-client" "$bap_docker_compose_file" "fidedocker/protocol-server"
        update_service "bap-network" "$bap_docker_compose_file" "fidedocker/protocol-server"
        ;;
    4)
        update_service "bpp-client" "$bpp_docker_compose_file" "fidedocker/protocol-server"
        update_service "bpp-network" "$bpp_docker_compose_file" "fidedocker/protocol-server"
        ;;
    5)
        update_service "registry" "$registry_docker_compose_file" "fidedocker/registry"
        update_service "gateway" "$gateway_docker_compose_file" "fidedocker/gateway"
        update_service "bap-client" "$bap_docker_compose_file" "fidedocker/protocol-server"
        update_service "bap-network" "$bap_docker_compose_file" "fidedocker/protocol-server"
        update_service "bpp-client" "$bpp_docker_compose_file" "fidedocker/protocol-server"
        update_service "bpp-network" "$bpp_docker_compose_file" "fidedocker/protocol-server"
        ;;
    *)
        echo "Unknown choice"
        ;;
    esac
}

# MAIN SCRIPT STARTS HERE

echo "Welcome to Beckn-ONIX!"
if [ -f ./onix_ascii_art.txt ]; then
    cat ./onix_ascii_art.txt
else
    echo "[Display Beckn-ONIX ASCII Art]"
fi

echo "Checking prerequisites of Beckn-ONIX deployment"
check_docker_permissions

echo "Beckn-ONIX is a platform that helps you quickly launch and configure beckn-enabled networks."
echo -e "\nWhat would you like to do?\n1. Join an existing network\n2. Create new production network\n3. Set up a network on your local machine\n4. Merge multiple networks\n5. Configure Existing Network\n6. Update/Upgrade Application\n(Press Ctrl+C to exit)"
read -p "Enter your choice: " choice

validate_input "$choice" 6
if [[ $? -ne 0 ]]; then
    restart_script # Restart the script if input is invalid
fi

if [[ $choice -eq 3 ]]; then
    echo "Installing all components on the local machine"
    install_registry
    install_gateway
    install_bap_protocol_server
    install_bpp_protocol_server_with_sandbox
elif [[ $choice -eq 4 ]]; then
    echo "Determining the platforms available based on the initial choice"
    mergingNetworks
elif [[ $choice -eq 5 ]]; then
    echo "${BoldGreen}Currently this feature is not available in this distribution of Beckn ONIX${NC}"
    restart_script
elif [[ $choice -eq 6 ]]; then
    update_network
else
    # Determine the platforms available based on the initial choice
    platforms=("Gateway" "BAP" "BPP" "ALL")
    [ "$choice" -eq 2 ] && platforms=("Registry" "${platforms[@]}") # Add Registry for new network setups

    echo "Great choice! Get ready."
    echo -e "\nWhich platform would you like to set up?"
    for i in "${!platforms[@]}"; do
        echo "$((i + 1)). ${platforms[$i]}"
    done

    read -p "Enter your choice: " platform_choice
    validate_input "$platform_choice" "${#platforms[@]}"
    if [[ $? -ne 0 ]]; then
        restart_script # Restart the script if input is invalid
    fi

    selected_platform="${platforms[$((platform_choice - 1))]}"

    if [[ -n $selected_platform ]]; then
        completeSetup "$selected_platform"
    else
        restart_script
    fi
fi

echo "Process complete. Thank you for using Beckn-ONIX!"
