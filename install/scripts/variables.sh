#!/bin/bash

#Colour Code
RED=$(tput setaf 1)
GREEN=$(tput setaf 2)
YELLOW=$(tput setaf 3)
BLUE=$(tput setaf 4)
NC=$(tput sgr0)

# Bold Colour Code
BOLD=$(tput bold)
BoldGreen="${BOLD}$(tput setaf 2)"
BoldRed="${BOLD}$(tput setaf 1)"

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

# Default registry and gateway username and password
USERNAME="root"
PASSWORD="root"

# Registry Role Permission file path
REGISTRY_FILE_PATH="../registry_data/RolePermission.xlsx"

#Comman Variables with Default values
mongo_initdb_root_username="beckn"
mongo_initdb_root_password="beckn123"
mongo_initdb_database="protocol_server"
mongoUrl="mongoDB:27017"

rabbitmq_default_user="beckn"
rabbitmq_default_pass="beckn123"
rabbitmqUrl="rabbitmq"

redisUrl="redis"

registry_url="http://registry:3030/subscribers"
beckn_registry_url="https://registry.becknprotocol.io/subscribers"

layer2_url=""
schemas_path="/usr/src/app/schemas"

#BAP varibales. 

bapClientFile="$SCRIPT_DIR/../protocol-server-data/bap-client.yaml-sample"
bapNetworkFile="$SCRIPT_DIR/../protocol-server-data/bap-network.yaml-sample"

bap_client_port=5001
bap_network_port=5002

bap_subscriber_id="bap-network"
bap_subscriber_key_id="bap-network-key"
bap_subscriber_url="http://bap-network:5002"
bap_client_url="http://bap-client:5002"

#BPP varibales. 

bppClientFile="$SCRIPT_DIR/../protocol-server-data/bpp-client.yaml-sample"
bppNetworkFile="$SCRIPT_DIR/../protocol-server-data/bpp-network.yaml-sample"

bpp_client_port=6001
bpp_network_port=6002

bpp_subscriber_id="bpp-network"
bpp_subscriber_key_id="bpp-network-key"
bpp_subscriber_url="http://bpp-network:6002"
webhook_url="http://sandbox-api:3000"

bpp_docker_compose_file=docker-compose-bpp.yml
bpp_docker_compose_file_sandbox=docker-compose-bpp-with-sandbox.yml
bap_docker_compose_file=docker-compose-bap.yml
registry_docker_compose_file=docker-compose-registry.yml
gateway_docker_compose_file=docker-compose-gateway.yml
gcl_docker_compose_file=docker-compose-gcl.yml