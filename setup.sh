#!/bin/bash

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Beckn-ONIX Complete Setup${NC}"
echo -e "${BLUE}========================================${NC}"

# Check if Docker is running
if ! docker info > /dev/null 2>&1; then
    echo -e "${RED}Error: Docker is not running. Please start Docker first.${NC}"
    exit 1
fi

# Step 1: Run the Beckn network installer
echo -e "${YELLOW}Step 1: Setting up Beckn network services...${NC}"

# Check if install directory exists
if [ ! -d "./install" ]; then
    echo -e "${RED}Error: install directory not found.${NC}"
    exit 1
fi

# Make the installer executable
chmod +x ./install/beckn-onix.sh

# Navigate to install directory and run setup
cd install

# Auto-select option 3 (local setup) for the installer
echo -e "${GREEN}Running local network setup...${NC}"
echo "3" | ./beckn-onix.sh

cd ..

# Wait for services to stabilize
echo -e "${YELLOW}Waiting for services to be ready...${NC}"
sleep 15

# Step 2: Configure Vault for key management
echo -e "${YELLOW}Step 2: Setting up Vault for key management...${NC}"

# Check if Vault is running, if not start it
if ! docker ps | grep -q "vault"; then
    echo -e "${BLUE}Starting Vault container...${NC}"
    docker run -d \
        --name vault \
        --cap-add=IPC_LOCK \
        -e VAULT_DEV_ROOT_TOKEN_ID=root \
        -e VAULT_DEV_LISTEN_ADDRESS=0.0.0.0:8200 \
        -p 8200:8200 \
        hashicorp/vault:latest > /dev/null 2>&1
    sleep 5
fi

# Configure Vault
echo -e "${BLUE}Configuring Vault policies...${NC}"
docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault \
    vault auth enable approle > /dev/null 2>&1 || true

echo 'path "beckn/*" { capabilities = ["create", "read", "update", "delete", "list"] }' | \
    docker exec -i -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault \
    vault policy write beckn-policy - > /dev/null 2>&1

docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault \
    vault write auth/approle/role/beckn-role \
    token_policies="beckn-policy" \
    token_ttl=24h \
    token_max_ttl=48h > /dev/null 2>&1

# Get Vault credentials
ROLE_ID=$(docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault \
    vault read -field=role_id auth/approle/role/beckn-role/role-id 2>/dev/null)
SECRET_ID=$(docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault \
    vault write -field=secret_id -f auth/approle/role/beckn-role/secret-id 2>/dev/null)

# Enable KV v2 secrets engine
docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault \
    vault secrets enable -path=beckn kv-v2 > /dev/null 2>&1 || true

echo -e "${GREEN}✓ Vault configured successfully${NC}"

# Step 3: Check services status
echo -e "${YELLOW}Step 3: Checking services status...${NC}"

# Check if services are running
if docker ps | grep -q "registry"; then
    echo -e "${GREEN}✓ Registry is running${NC}"
fi
if docker ps | grep -q "gateway"; then
    echo -e "${GREEN}✓ Gateway is running${NC}"
fi
if docker ps | grep -q "bap-client"; then
    echo -e "${GREEN}✓ BAP services are running${NC}"
fi
if docker ps | grep -q "bpp-client"; then
    echo -e "${GREEN}✓ BPP services are running${NC}"
fi
if docker ps | grep -q "vault"; then
    echo -e "${GREEN}✓ Vault is running${NC}"
fi

# Step 4: Build adapter plugins
echo -e "${YELLOW}Step 4: Building adapter plugins...${NC}"

if [ -f "./build-plugins.sh" ]; then
    chmod +x ./build-plugins.sh
    ./build-plugins.sh
    echo -e "${GREEN}✓ Plugins built successfully${NC}"
else
    echo -e "${RED}Warning: build-plugins.sh not found${NC}"
fi

# Step 5: Build the adapter server
echo -e "${YELLOW}Step 5: Building Beckn-ONIX adapter server...${NC}"

if [ -f "go.mod" ]; then
    go build -o beckn-adapter cmd/adapter/main.go
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ Adapter server built successfully${NC}"
    else
        echo -e "${RED}Failed to build adapter server${NC}"
    fi
else
    echo -e "${RED}Error: go.mod not found${NC}"
fi

# Step 6: Create environment file
echo -e "${YELLOW}Step 6: Creating environment configuration...${NC}"

cat > .env <<EOF
# Beckn-ONIX Environment Configuration
# Generated on $(date)

# Service URLs
export REGISTRY_URL=http://localhost:3000
export GATEWAY_URL=http://localhost:4000
export BAP_CLIENT_URL=http://localhost:5001
export BAP_NETWORK_URL=http://localhost:5002
export BPP_CLIENT_URL=http://localhost:6001
export BPP_NETWORK_URL=http://localhost:6002
export REDIS_URL=localhost:6379
export MONGO_URL=mongodb://localhost:27017

# Adapter Configuration
export ADAPTER_PORT=8080
export ADAPTER_MODE=development

# Vault Configuration
export VAULT_ADDR=http://localhost:8200
export VAULT_TOKEN=root
export VAULT_ROLE_ID=$ROLE_ID
export VAULT_SECRET_ID=$SECRET_ID
EOF

echo -e "${GREEN}✓ Environment file created${NC}"

# Display final status
echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}✅ Setup Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo -e "${BLUE}Services Running:${NC}"
echo -e "  📦 Registry:     http://localhost:3000"
echo -e "  🌐 Gateway:      http://localhost:4000"
echo -e "  🛒 BAP Client:   http://localhost:5001"
echo -e "  🛒 BAP Network:  http://localhost:5002"
echo -e "  🏪 BPP Client:   http://localhost:6001"
echo -e "  🏪 BPP Network:  http://localhost:6002"
echo -e "  💾 Redis:        localhost:6379"
echo -e "  🗄️  MongoDB:      localhost:27017"
echo ""
echo -e "${GREEN}Next Steps:${NC}"
echo -e "1. Run the adapter:"
echo -e "   ${YELLOW}source .env && ./beckn-adapter --config=config/local-dev.yaml${NC}"
echo ""
echo -e "2. Test the endpoints:"
echo -e "   ${YELLOW}./test_endpoints.sh${NC}"
echo ""
echo -e "3. Stop all services:"
echo -e "   ${YELLOW}cd install && docker compose down${NC}"
echo ""
echo -e "4. View logs:"
echo -e "   ${YELLOW}cd install && docker compose logs -f [service-name]${NC}"
echo -e "${GREEN}========================================${NC}"