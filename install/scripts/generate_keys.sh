#!/bin/bash
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
source $SCRIPT_DIR/variables.sh

# Run the script that generates keys and capture the output
get_keys() {
    docker pull fidedocker/protocol-server > /dev/null 2>&1
    docker run --name temp -itd fidedocker/protocol-server > /dev/null 2>&1
    output=$(docker exec -i temp node /usr/src/app/scripts/generate-keys 2>&1)
    docker stop temp > /dev/null 2>&1
    docker rm temp > /dev/null 2>&1
# Check if the script executed successfully
if [ $? -eq 0 ]; then
    # Extract Public Key and Private Key using grep and awk
    public_key=$(echo "$output" | awk '/Your Public Key/ {getline; print $0}')
    private_key=$(echo "$output" | awk '/Your Private Key/ {getline; print $0}')
    # Remove leading and trailing whitespaces
    public_key=$(echo "$public_key" | tr -d '[:space:]')
    private_key=$(echo "$private_key" | tr -d '[:space:]')

else
    # Print an error message if the script failed
    echo "${RED}Error: Key generation script failed. Please check the script output.${NC}"
fi
}

#get_keys
