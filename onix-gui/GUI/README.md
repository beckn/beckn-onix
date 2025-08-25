# beckn-onix-gui

The GUI for the beckn-onix cli tool.

## Pre-requisites

1. 4 server/instances
2. 4 sub-domains mapped to each instance
3. Local Tunnel
   `npm i -g localtunnel`

## User Guide

### Step 1: Run the `start.sh` script

```
cd .. && ./start.sh
```

### Step 2: Accessing the GUI.

You will be getting a URL and password as on output of the script. Open the url in the browser and then
paste the password.

### Step 3: Install and setup your network

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

### Made with ❤️

built by the [Mulearn Team](https://mulearn.org/)

1. [Mishal Abdullah](https://github.com/Mishalabdullah/)
2. [Aswin Asok](https://github.com/AswinAsok)
3. [Viraj Prabhu ](https://github.com/viraka)
4. [Adarsh Mohan](https://www.linkedin.com/in/adarshmohanks/)
