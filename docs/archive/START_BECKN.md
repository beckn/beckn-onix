# Beckn-ONIX Setup Script

## Overview

This shell script, `start_beckn_v2.sh`, automates the setup of Beckn components, including the Registry, Gateway, Protocol Server BAP, Protocol Server BPP, Sandbox, Webhook, and supporting services such as MongoDB, Redis, and RabbitMQ.

## How to Use

1. **Clone the Repository:**

   ```bash
   git clone -b main https://github.com/beckn/onix.git
   ```

2. **Navigate to the Script Directory:**

   ```bash
   cd onix/install
   ```

3. **Run the Setup Script:**

   ```bash
   ./start_beckn_v2.sh
   ```

   The script will guide you through the installation.

## Installation Sequence - Design

1. **Install Required Packages:**
   It will install Docker, Docker-Compose, and jq packages which are required for this setup.

   ```bash
   ./package_manager.sh
   ```

2. **Install Registry Service:**

   ```bash
   ./start_container registry
   ```

3. **Install Gateway Service:**

   ```bash
   ./update_gateway_details.sh registry
   ./start_container gateway
   ./register_gateway.sh
   ```

4. **Start Supporting Services:**

   - MongoDB
   - RabbitMQ
   - Redis

   ```bash
   ./start_support_services
   ```

5. **Install Protocol Server for BAP:**

   ```bash
   ./update_bap_config.sh
   ./start_container "bap-client"
   ./start_container "bap-network"
   ```

6. **Install Sandbox:**

   ```bash
   ./start_container "sandbox-api"
   ```

7. **Install Protocol Server for BPP:**

   ```bash
   ./update_bpp_config.sh
   ./start_container "bpp-client"
   ./start_container "bpp-network"
   ```

## Post-Installation Details

Upon successful execution, the script provides the following details for use in the Postman collection:
For Example

```bash
BASE_URL=http://172.18.0.7:5001/
BAP_ID=bap-network
BAP_URI=http://172.18.0.11:5002/
BPP_ID=bpp-network
BPP_URI=http://172.18.0.12:6002/
```
