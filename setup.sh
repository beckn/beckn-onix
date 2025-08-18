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

# Step 1: Start all services with docker-compose
echo -e "${YELLOW}Step 1: Starting all Beckn network services...${NC}"
docker compose down 2>/dev/null
docker compose up -d

# Wait for services to be ready
echo -e "${YELLOW}Waiting for services to be ready...${NC}"
sleep 10

# Step 2: Configure Vault
echo -e "${YELLOW}Step 2: Configuring Vault for key management...${NC}"

# Wait for Vault to be ready
for i in {1..30}; do
    if docker exec -e VAULT_ADDR=http://127.0.0.1:8200 vault vault status > /dev/null 2>&1; then
        echo -e "${GREEN}Vault is ready!${NC}"
        break
    fi
    if [ $i -eq 30 ]; then
        echo -e "${RED}Error: Vault failed to start${NC}"
        exit 1
    fi
    sleep 1
done

# Configure Vault
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

# Get credentials
ROLE_ID=$(docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault \
    vault read -field=role_id auth/approle/role/beckn-role/role-id 2>/dev/null)
SECRET_ID=$(docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault \
    vault write -field=secret_id -f auth/approle/role/beckn-role/secret-id 2>/dev/null)

# Enable KV v2 secrets engine
docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault \
    vault secrets enable -path=beckn kv-v2 > /dev/null 2>&1 || true

# Store sample keys
docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault \
    vault kv put beckn/keys/bap \
    private_key='sample_bap_private_key' \
    public_key='sample_bap_public_key' > /dev/null 2>&1

docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=root vault \
    vault kv put beckn/keys/bpp \
    private_key='sample_bpp_private_key' \
    public_key='sample_bpp_public_key' > /dev/null 2>&1

# Step 3: Build plugins
echo -e "${YELLOW}Step 3: Building plugins...${NC}"
if [ -f "./build-plugins.sh" ]; then
    chmod +x ./build-plugins.sh
    ./build-plugins.sh
else
    echo -e "${RED}Warning: build-plugins.sh not found. Please build plugins manually.${NC}"
fi

# Step 4: Build server
echo -e "${YELLOW}Step 4: Building Beckn-ONIX server...${NC}"
go build -o server cmd/adapter/main.go

# Create .env.vault file
echo -e "${YELLOW}Step 5: Creating environment file...${NC}"
cat > .env.vault <<EOF
# Vault Credentials for Beckn-ONIX
# Generated on $(date)
export VAULT_ROLE_ID=$ROLE_ID
export VAULT_SECRET_ID=$SECRET_ID
EOF

# Display status
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
echo -e "  🔐 Vault UI:     http://localhost:8200 (token: root)"
echo -e "  💾 Redis:        localhost:6379"
echo ""
echo -e "${GREEN}To run the Beckn-ONIX server:${NC}"
echo "  source .env.vault && ./server --config=config/local-dev.yaml"
echo ""
echo -e "${GREEN}To stop all services:${NC}"
echo "  docker compose down"
echo ""
echo -e "${GREEN}To view logs:${NC}"
echo "  docker compose logs -f [service-name]"
echo -e "${GREEN}========================================${NC}"