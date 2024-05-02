# beckn-onix-gui

The GUI for the beckn-onix cli tool.

## Pre-requisites

1. 4 server/instances
2. 4 sub-domains mapped to each instance

## User Guide

### Step 1: Clone the beckn-onix-gui repo

```
git clone https://github.com/Mishalabdullah/beckn-onix-gui.git
```

### Step 2: Change directory to the nextjs project

```
cd onix-gui
```

### Step 3: Run nextjs on port 3005

For running the installation script just run this command. The sudo privilages are required when installing packages and configuring the nginx reverse proxy

```
sudo ./start.sh
```

Note: Port 3000 is being used by onix, so we're using port 3005 instead.

### Step 4: Configure a reverse proxy using NGINX

Map port 3005 to a domain or use your machine IP:3005 to access the GUI from your browser.

### Step 5: Secure with certbot

Use certbot to obtain an SSL certificate and secure your GUI.

## Contributing

Contributions are welcome! If you'd like to contribute to the beckn-onix-gui project, please fork the repo and submit a pull request.

## License

The beckn-onix-gui project is licensed under the MIT License. See the LICENSE file for more information.

## Contact

If you have any questions or issues with the beckn-onix-gui project, please don't hesitate to reach out.
