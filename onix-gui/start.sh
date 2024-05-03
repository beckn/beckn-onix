#!/bin/bash

# Set script variables
PROJECT_DIR="GUI"
PORT=3005
TUNNEL_SERVICE="lt"

# Change to the project directory
cd "$PROJECT_DIR" || exit
npm i &&
# Build and start the Next.js app
echo "installing Dependencies"

echo "Building and starting Next.js app..."
npx next build &&
echo "Builing Web App = True"
sleep 3
npx next start -p "$PORT" > /dev/null 2>&1 &

# Wait for the Next.js app to start
echo "Waiting for Next.js app to start on port $PORT..."
until nc -z localhost "$PORT"; do
  sleep 1
  echo "Loding ..."
done

# Install the tunnel service if not installed



echo "Exposing local port $PORT using $TUNNEL_SERVICE..."
lt --port "$PORT" > /tmp/lt.log 2>&1 &

# Wait for the tunnel service to start
echo "Waiting for tunnel service to start..."
sleep 5

# Get the tunnel URL from the log file
TUNNEL_URL=$(grep -o 'https://[^[:blank:]]*' /tmp/lt.log)
#Get the tunnel password
echo "Getting Tunnel Password"
TUNNEL_PASSWORD=$(curl https://loca.lt/mytunnelpassword)&&

# Print the tunnel URL and password
echo "---------------------------------------"
echo "Next.js app is running locally on port $PORT"
echo "Tunnel Service URL: $TUNNEL_URL"
echo "Tunnel Password: $TUNNEL_PASSWORD"
echo "---------------------------------------"
