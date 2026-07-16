#!/bin/bash

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# UNUSED FUNCTION - Complete Vault setup kept for future reference
setup_vault_unused() {
    echo -e "${YELLOW}Setting up Vault for key management...${NC}"
    
    if ! docker ps | grep -q "vault"; then
        echo -e "${BLUE}Starting Vault container...${NC}"
        docker run -d \
            --name vault \
            --cap-add=IPC_LOCK \
            -e VAULT_DEV_ROOT_TOKEN_ID=root \
            -e VAULT_DEV_LISTEN_ADDRESS=0.0.0.0:8200 \
            -p 8200:8200 \
            hashicorp/vault:latest > /dev/null 2>&1
        
        for i in {1..30}; do
            if docker exec -e VAULT_ADDR=http://127.0.0.1:8200 vault vault status > /dev/null 2>&1; then
                echo -e "${GREEN}✓ Vault is ready${NC}"
                break
            fi
            if [ $i -eq 30 ]; then
                echo -e "${RED}Error: Vault failed to start${NC}"
                exit 1
            fi
            sleep 1
        done
    fi
    
    # Enable AppRole authentication
    docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault vault auth enable approle 2>/dev/null
    
    # Create policy for Beckn
    echo 'path "beckn/*" { capabilities = ["create", "read", "update", "delete", "list"] }' | docker exec -i -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault vault policy write beckn-policy - > /dev/null 2>&1
    
    # Create AppRole
    docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault vault write auth/approle/role/beckn-role token_policies="beckn-policy" token_ttl=24h token_max_ttl=48h > /dev/null 2>&1
    
    # Enable KV secrets engine
    docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault vault secrets enable -path=beckn kv-v2 > /dev/null 2>&1
    
    # Store BAP network keys
    docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault vault kv put secret/keys/bap-network signingPublicKey='1ct6/Xg6gHhT9QolufThbY4mWHYkIpXzh7YxMFM8MQE=' signingPrivateKey='C2hPMyeN+1Vzn8+7F/MUHmR5jKFuSb7s6tf/U5qni8vVy3r9eDqAeFP1CiW59OFtjiZYdiQilfOHtjEwUzwxAQ==' > /dev/null 2>&1
    
    # Get AppRole credentials
    ROLE_ID=$(docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault vault read -field=role_id auth/approle/role/beckn-role/role-id)
    SECRET_ID=$(docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault vault write -field=secret_id -f auth/approle/role/beckn-role/secret-id)
    
    echo -e "${GREEN}✓ Vault setup complete${NC}"
    echo -e "${BLUE}Role ID: ${ROLE_ID}${NC}"
    echo -e "${BLUE}Secret ID: ${SECRET_ID}${NC}"
}

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Beckn-ONIX Complete Setup${NC}"
echo -e "${BLUE}========================================${NC}"

# Check if Docker is running
if ! docker info > /dev/null 2>&1; then
    echo -e "${RED}Error: Docker is not running. Please start Docker first.${NC}"
    exit 1
fi

# Step 1: Start dependent services (Redis only)
echo -e "${YELLOW}Step 1: Starting dependent services...${NC}"
export COMPOSE_IGNORE_ORPHANS=1
docker compose -f ./docker-compose-adapter.yml down 2>/dev/null
docker compose -f ./docker-compose-adapter.yml up -d redis
echo "Redis installation successful"

# Make the installer executable
#chmod +x ./beckn-onix.sh

# Auto-select option 3 (local setup) for the installer
#echo -e "${GREEN}Running local network setup...${NC}"
#echo "3" | ./beckn-onix.sh

cd ..

# Step 2: Create required directories
echo -e "${YELLOW}Step 2: Creating required directories...${NC}"

# Create schemas directory for validation
if [ ! -d "schemas" ]; then
    mkdir -p schemas
    echo -e "${GREEN}✓ Created schemas directory${NC}"
else
    echo -e "${YELLOW}schemas directory already exists${NC}"
fi

# Create logs directory
if [ ! -d "logs" ]; then
    mkdir -p logs
    echo -e "${GREEN}✓ Created logs directory${NC}"
else
    echo -e "${YELLOW}logs directory already exists${NC}"
fi

# Create plugins directory if not exists
if [ ! -d "plugins" ]; then
    mkdir -p plugins
    echo -e "${GREEN}✓ Created plugins directory${NC}"
else
    echo -e "${YELLOW}plugins directory already exists${NC}"
fi

# Compute build-time identity vars once, then export them so the plugin
# build (Step 3) and the adapter build (Step 4) embed the exact same
# version/commit/tree-state/build-date -- including the otelsetup plugin,
# which otherwise silently ships with pkg/version's "dev"/"unknown"
# defaults since it's compiled as a separate .so.
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
source "${SCRIPT_DIR}/scripts/version-vars.sh"
export ONIX_VERSION GIT_COMMIT GIT_TREE_STATE BUILD_DATE

# Step 3: Build adapter plugins
echo -e "${YELLOW}Step 3: Building adapter plugins...${NC}"

if [ -f "./install/build-plugins.sh" ]; then
    chmod +x ./install/build-plugins.sh
    ./install/build-plugins.sh
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ Plugins built successfully${NC}"
    else
        echo -e "${RED}Error: Plugin build failed${NC}"
        exit 1
    fi
else
    echo -e "${RED}Error: install/build-plugins.sh not found${NC}"
    exit 1
fi

# Step 4: Build the adapter server
echo -e "${YELLOW}Step 4: Building Beckn-ONIX adapter server...${NC}"

if [ -f "go.mod" ]; then
    go build -ldflags "${ONIX_LDFLAGS}" -o beckn-adapter cmd/adapter/main.go
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ Adapter server built successfully${NC}"
    else
        echo -e "${RED}Error: Failed to build adapter server${NC}"
        echo -e "${YELLOW}Please check Go installation and dependencies${NC}"
        exit 1
    fi
else
    echo -e "${RED}Error: go.mod not found${NC}"
    exit 1
fi

# Step 5: Start ONIX Adapter
echo -e "${YELLOW}Step 5: Starting ONIX Adapter...${NC}"
cd install
docker compose -f ./docker-compose-adapter.yml up -d onix-adapter
echo "ONIX Adapter installation successful"
cd ..

# Step 6: Check services status
echo -e "${YELLOW}Step 6: Checking services status...${NC}"

# Check if services are running
if docker ps | grep -q "redis"; then
    echo -e "${GREEN}✓ Redis is running${NC}"
fi
if docker ps | grep -q "onix-adapter"; then
    echo -e "${GREEN}✓ ONIX Adapter is running${NC}"
fi

# Step 7: Create environment file
echo -e "${YELLOW}Step 7: Creating environment configuration...${NC}"

cat > .env <<EOF
# Beckn-ONIX Environment Configuration
# Generated on $(date)

# Service URLs
export REDIS_URL=localhost:6379

# Adapter Configuration
export ADAPTER_PORT=8081
export ADAPTER_MODE=development
EOF

if [ -f ".env" ]; then
    echo -e "${GREEN}✓ Environment file created successfully${NC}"
else
    echo -e "${RED}Error: Failed to create .env file${NC}"
    exit 1
fi

# Display final status
echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}✅ Setup Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo -e "${BLUE}Services Running:${NC}"
echo -e "  💾 Redis:        localhost:6379"
echo -e "  🔧 ONIX Adapter: localhost:8081"
echo ""
echo -e "${GREEN}Next Steps:${NC}"
echo -e "1. Adapter is running in Docker at 8081"
echo -e "2. Optionally, if you want to run adapter locally (update config file /config to suit to your environment ) then run below command:"
echo -e "   ${YELLOW}source .env && ./beckn-adapter --config=config/<your-config>.yaml${NC}"
echo ""
echo -e "3. Test the endpoints:"
echo -e "   ${YELLOW}curl -X POST http://localhost:8081/bap/caller/search${NC}"
echo ""
echo -e "4. Stop all services:"
echo -e "   ${YELLOW}cd install && docker compose -f docker-compose-adapter.yml down ${NC}"
echo ""
echo -e "5. View logs:"
echo -e "   ${YELLOW}cd install && docker compose -f docker-compose-adapter.yml logs -f onix-adapter${NC}"
echo -e "${GREEN}========================================${NC}"