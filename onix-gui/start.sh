#!/bin/bash

# Installing dependencies

# Execute the package_manager.sh script to handle Docker installation
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
source "../install/scripts/package_manager.sh"

# Run the package_manager.sh script without passing any arguments
# The script will handle the installation of required packages
# including Docker and Docker Compose as defined within it.
# No need to call install_package function explicitly.

echo "installing node"
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.39.7/install.sh | bash &&
source ~/.bashrc &&
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh" # This loads nvm
[ -s "$NVM_DIR/bash_completion" ] && \. "$NVM_DIR/bash_completion" # This loads nvm bash_completion
nvm install 20 &&
npm i -g localtunnel &&

# Add user to the docker group and apply permissions
sudo groupadd docker & 
sudo usermod -aG docker $USER & 
newgrp docker &

# Set script variables
PROJECT_DIR="GUI"
PORT=3005
TUNNEL_SERVICE="lt"

# Change to the project directory
cd "$PROJECT_DIR" || exit
nvm use 20 &&
npm i &&

# Build and start the Next.js app
echo "installing Dependencies"
echo "Building and starting Next.js app..."
npx next build &&
echo "Builing Web App = True"
sleep 3
npx next start -p "$PORT" &

# Wait for the Next.js app to start

# Install the tunnel service if not installed
sleep 3
echo "Exposing local port $PORT using $TUNNEL_SERVICE..."
lt --port "$PORT" > /tmp/lt.log 2>&1 &

# Wait for the tunnel service to start
echo "Waiting for tunnel service to start..."
sleep 5

# Get the tunnel URL from the log file
TUNNEL_URL=$(grep -o 'https://[^[:blank:]]*' /tmp/lt.log)

#Get the tunnel password
echo "Getting Tunnel Password"
TUNNEL_PASSWORD=$(curl https://loca.lt/mytunnelpassword) &&

# Print the tunnel URL and password
echo "---------------------------------------"
echo "Next.js app is running locally on port $PORT"
echo "Tunnel Service URL: $TUNNEL_URL"
echo "Tunnel Password: $TUNNEL_PASSWORD"
echo "---------------------------------------"
