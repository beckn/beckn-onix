#!/bin/bash

# Load Variables
source ../variables.sh

# Define Kubernetes manifest paths
CONFIGMAP_FILE="bpp-config.yaml"
DEPLOYMENT_BAP="bap-client-deployment.yaml"
DEPLOYMENT_BPP="bpp-client-deployment.yaml"
DEPLOYMENT_REGISTRY="./registry/registry_deployment.yaml"
DEPLOYMENT_GATEWAY="gateway-deployment.yaml"
SERVICE_BAP="bap-service.yaml"
SERVICE_BPP="bpp-service.yaml"
SERVICE_REGISTRY="./registry/registry_service.yaml"
SERVICE_GATEWAY="gateway-service.yaml"

# Function to check prerequisites
check_prerequisites() {
    echo "Checking prerequisites of Beckn-ONIX deployment..."

    # Check if Docker is installed
    if ! command -v docker &> /dev/null; then
        echo "Docker is not installed. Please install Docker and retry."
        exit 1
    fi

    # Check if kubectl is installed
    if ! command -v kubectl &> /dev/null; then
        echo "kubectl is not installed. Please install kubectl and retry."
        exit 1
    fi

    echo "All prerequisites are met."
}

# Function to update Kubernetes manifest files
update_manifests() {
    local file=$1
    echo "Updating Kubernetes manifest: $file"

    sed -i "s|REGISTRY_URL|$registry_url|g" $file
    sed -i "s|REGISTRY_PORT|3030|g" $file
    sed -i "s|GATEWAY_URL|$gateway_url|g" $file
    sed -i "s|GATEWAY_PORT|4000|g" $file
    sed -i "s|BPP_CLIENT_PORT|$bpp_client_port|g" $file
    sed -i "s|BPP_NETWORK_PORT|$bpp_network_port|g" $file
    sed -i "s|REDIS_URL|$redisUrl|g" $file
    sed -i "s|MONGO_USERNAME|$mongo_initdb_root_username|g" $file
    sed -i "s|MONGO_PASSWORD|$mongo_initdb_root_password|g" $file
    sed -i "s|MONGO_URL|$mongoUrl|g" $file
    sed -i "s|MONGO_DB_NAME|$mongo_initdb_database|g" $file
    sed -i "s|PRIVATE_KEY|your-private-key|g" $file
    sed -i "s|PUBLIC_KEY|your-public-key|g" $file
    sed -i "s|BPP_SUBSCRIBER_ID|$bpp_subscriber_id|g" $file
    sed -i "s|BPP_SUBSCRIBER_URL|$bpp_subscriber_url|g" $file
    sed -i "s|BPP_SUBSCRIBER_KEY_ID|$bpp_subscriber_key_id|g" $file
    sed -i "s|WEBHOOK_URL|$webhook_url|g" $file
    sed -i "s|RABBITMQ_USERNAME|$rabbitmq_default_user|g" $file
    sed -i "s|RABBITMQ_PASSWORD|$rabbitmq_default_pass|g" $file
    sed -i "s|RABBITMQ_URL|$rabbitmqUrl|g" $file
    sed -i "s|USE_LAYER_2_CONFIG|false|g" $file
    sed -i "s|MANDATE_LAYER_2_CONFIG|false|g" $file

    echo "Manifest updated successfully."
}

# Function to apply Kubernetes manifests
apply_k8s_manifests() {
    echo "Applying updated Kubernetes manifests..."
    kubectl apply -f $CONFIGMAP_FILE
    kubectl apply -f $DEPLOYMENT_BAP
    kubectl apply -f $DEPLOYMENT_BPP
    kubectl apply -f $DEPLOYMENT_REGISTRY
    kubectl apply -f $DEPLOYMENT_GATEWAY
    kubectl apply -f $SERVICE_BAP
    kubectl apply -f $SERVICE_BPP
    kubectl apply -f $SERVICE_REGISTRY
    kubectl apply -f $SERVICE_GATEWAY
    echo "Kubernetes manifests applied successfully."
}

# Main menu
main_menu() {
    echo ""
    echo "Beckn-ONIX is a platform that helps you quickly launch and configure beckn-enabled networks."
    echo ""
    echo "What would you like to do?"
    echo "1. Join an existing network"
    echo "2. Create new production network"
    echo "3. Set up a network on your local machine"
    echo "4. Merge multiple networks"
    echo "5. Configure Existing Network"
    echo "6. Update/Upgrade Application"
    echo "(Press Ctrl+C to exit)"
    read -p "Enter your choice: " main_choice

    case $main_choice in
        1) echo "Joining an existing network... (To be implemented)";;
        2) setup_platform_menu ;;
        3) echo "Setting up network on local machine... (To be implemented)";;
        4) echo "Merging multiple networks... (To be implemented)";;
        5) echo "Configuring existing network... (To be implemented)";;
        6) echo "Updating/Upgrading application... (To be implemented)";;
        *) echo "Invalid choice. Please select a valid option." && main_menu;;
    esac
}

# Setup menu
setup_platform_menu() {
    echo ""
    echo "Great choice. Get ready."
    echo ""
    echo "Which platform would you like to set up?"
    echo "1. Registry"
    echo "2. Gateway"
    echo "3. BAP"
    echo "4. BPP"
    echo "5. ALL"
    read -p "Enter your choice: " platform_choice

    case $platform_choice in
        1) update_manifests $DEPLOYMENT_REGISTRY ;;
        2) update_manifests $DEPLOYMENT_GATEWAY ;;
        3) update_manifests $DEPLOYMENT_BAP ;;
        4) update_manifests $DEPLOYMENT_BPP ;;
        5) 
            update_manifests $DEPLOYMENT_REGISTRY
            update_manifests $DEPLOYMENT_GATEWAY
            update_manifests $DEPLOYMENT_BAP
            update_manifests $DEPLOYMENT_BPP
            ;;
        *) echo "Invalid choice. Please select a valid option." && setup_platform_menu ;;
    esac

    read -p "Do you want to apply the changes to Kubernetes? (y/n): " apply_choice
    if [[ $apply_choice == "y" ]]; then
        apply_k8s_manifests
    fi
}

# Start the script
check_prerequisites
main_menu