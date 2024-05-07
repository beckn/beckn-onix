#!/bin/bash
source ./scripts/variables.sh
source ./scripts/get_container_details.sh

#below function will start specifice service inside docker-compose file
start_container(){
    echo "$1"
    docker-compose up -d $1

}

#below function will start the MongoDB, Redis and RabbitMQ Services. 
start_support_services(){
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
# Main script starts here
text="
Welcome to Beckn-ONIX!
The following components will be installed

1. MongoDB, RabbitMQ and Redis
2. Registry
3. Gateway
4. Sandbox
5. Sandbox Webhook
6. Protocol Server for BAP
7. Protocol Server for BPP
"
echo "$text"
sleep 5
echo "${GREEN}................Installing required packages................${NC}"
./scripts/package_manager.sh
echo "Package Installation is done"

export COMPOSE_IGNORE_ORPHANS=1

echo "${GREEN}................Installing Registry service................${NC}"
start_container registry
sleep 10
echo "Registry installation successful"
sleep 5
./scripts/update_gateway_details.sh registry
echo "${GREEN}................Installing Gateway service................${NC}"
start_container gateway
echo "Registering Gateway in the registry"
sleep 5
./scripts/register_gateway.sh
echo " "
echo "Gateway installation successful"

#Start the MongoDB, Redis and RabbitMQ Services.
start_support_services
sleep 10

echo "${GREEN}................Installing Protocol Server for BAP................${NC}"
./scripts/update_bap_config.sh
sleep 10
start_container "bap-client"
start_container "bap-network"
sleep 10
echo "Protocol server BAP installation successful"

echo "${GREEN}................Installing Sandbox................${NC}"
start_container "sandbox-api"
sleep 5
echo "Sandbox installation successful"

echo "${GREEN}................Installing Webhook................${NC}"
start_container "sandbox-webhook"
sleep
echo "Webhook installation successful"

echo "${GREEN}................Installing Protocol Server for BPP................${NC}"
bash scripts/update_bpp_config.sh
sleep 10
start_container "bpp-client"
start_container "bpp-network"
sleep 10
echo "Protocol server BPP installation successful"

if [[ $(uname -s) == 'Darwin' ]]; then
    ip=localhost
    bap_network_ip=$ip
    bap_client_ip=$ip
    bpp_network_ip=$ip
    bap_network_ip=$ip
elif [[ $(systemd-detect-virt) == 'wsl' ]]; then
    ip=$(hostname -I | awk '{print $1}')
    bap_network_ip=$ip
    bap_client_ip=$ip
    bpp_network_ip=$ip
    bap_network_ip=$ip
else
    bap_network_ip=$(get_container_ip bap-network)
    bap_client_ip=$(get_container_ip bap-client)
    bpp_network_ip=$(get_container_ip bpp-network)
    bap_network_ip=$(get_container_ip bpp-client)
fi

echo " "
echo "##########################################################"
echo "${GREEN}Please find below details of protocol server which required in postman collection${NC}"
echo "BASE_URL=http://$bap_client_ip:$bap_client_port/"
echo "BAP_ID=$bap_subscriber_id"
echo "BAP_URI=http://$bap_network_ip:$bap_network_port/"
echo "BPP_ID=$bpp_subscriber_id"
echo "BPP_URI=http://$bpp_network_ip:$bpp_network_port/"