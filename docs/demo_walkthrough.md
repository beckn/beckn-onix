# Steps to setup a new Beckn network and conduct transactions on it

## Introduction

This document describes setting up of a [Beckn network](https://becknprotocol.io/) with Beckn-ONIX and conducting transactions in a couple of domains (retail and energy). The general flow will involve the following steps:

- [Setup the prerequisites](#overall-prerequisites)
- [Create a new network and install the registry](#create-a-new-network-and-install-the-registry)
- [Install a gateway for the network](#install-a-gateway-for-the-network)
- [Install a Beckn Adaptor for the BAP](#install-a-beckn-adaptor-for-the-bap)
- [Install a Beckn Adaptor for the BPP](#install-a-beckn-adaptor-for-the-bpp)
- [Change the status of the BAP and BPP on the registry as Subscribed](#change-the-status-of-the-bap-and-bpp-on-registry-to-subscribed)
- [Update BAP and BPP with the layer 2 configuration files for the domains we are interested in](#update-bap-and-bpp-with-the-layer-2-configuration-files-for-the-domains-we-are-interested-in)
- [Conduct successful transactions on the network](#conduct-successful-transactions-on-the-network)

For the sake of illustration, all the urls are shown as subdomains of becknprotocol.io. These will not be available for you to configure on your network. When you are installing on your network, replace them with your own domain name. For example when the instruction below says "https://onix-registry.becknprotocol.io", if you own a domain "example.org", then what you enter will be "https://onix-registry.example.org". Of course you can give a different subdomain than `onix-registry`. However you should be consistent in using the same URL wherever registry url is required.

Some of the outputs listed below might be different when you run the script for the first time. The output depends on whether the required docker containers are present in the machine or not.

Note: Due to a [known issue](https://github.com/beckn/beckn-onix/issues/11), on certain machines, when the script is run for the first time, it errors out complaining about permission error in accessing docker daemon. Till this issue is fixed, the work around is to exit the terminal and restart the installation in a new terminal.

Please refer to the [Beckn-ONIX User Guide](./user_guide.md) for detailed explanation of the below steps.

## Sample deployment diagram

The following diagram shows a conceptual view of a multi-node Bekn network that we will be setting up. The urls shown here are the same as those used in the examples.

![Typical deployment](./images/sample_deployment.png)

## Overall prerequisites

- Setup the following subdomains at the registrar. Refer to [registering or adding domain or subdomain section](./user_guide.md/#appendix-a---registering-or-adding-domain-or-subdomains)

  - https://onix-registry.becknprotocol.io - point to machine with registry
  - https://onix-gateway.becknprotocol.io - point to machine with gateway
  - https://onix-bap-client.becknprotocol.io - point to machine with BAP
  - https://onix-bap.becknprotocol.io - point to machine with BAP
  - https://onix-bpp-client.becknprotocol.io - point to machine with BPP
  - https://onix-bpp.becknprotocol.io - point to machine with BPP

- Configure the reverse proxy to have the right ssl certificate installed for all the addresses above. Refer to [configuring ssl certificates on in reverse proxy](./user_guide.md/#ssl-certificates-configured-in-reverse-proxy) for more details
- Configure the reverse proxy with proxy_pass to configure the following routes. Refer to [configuring reverse proxy using proxy_pass](./user_guide.md/#configuring-nginx-reverse-proxy-using-proxy-pass) for details.

  - https://onix-registry.becknprotocol.io to port 3030 on the machine
  - https://onix-gateway.becknprotocol.io to port 4030 on the machine
  - https://onix-bap-client.becknprotocol.io to port 5001 on the machine
  - https://onix-bap.becknprotocol.io to port 5002 on the machine
  - https://onix-bpp-client.becknprotocol.io to port 6001 on the machine
  - https://onix-bpp.becknprotocol.io to port 6002 on the machine

- This guide assumes you have a marketplace or a headless store and want to set it up to work with the Beckn network. It is still useful for people who are developing the buyer side software and want to set it up with the network. In such cases a [sandbox](https://github.com/beckn/beckn-sandbox) might be required to mimic a marketplace or a headless shop.

## Create a new network and install the registry

- ssh into the virtual server that will hold the registry, clone the repo, change into the install folder and run the beckn-onix.sh script.

```
git clone https://github.com/beckn/beckn-onix.git
cd beckn-onix/install
./beckn-onix.sh
```

- In the prompt that comes up, choose setting up a new network.

```
Beckn-ONIX is a platform that helps you quickly launch and configure beckn-enabled networks.

What would you like to do?
1. Join an existing network
2. Create new production network
3. Set up a network on your local machine
4. Merge multiple networks
5. Configure Existing Network
(Press Ctrl+C to exit)
Enter your choice: 2

```

- Further choose Registry as the platform you want to install

```
Which platform would you like to set up?
1. Registry
2. Gateway
3. BAP
4. BPP
Enter your choice: 1
```

- Skip the option to apply network configuration

```
Proceeding with the setup for Registry...
Please provide the network-specific configuration URL.
Paste the URL of the network configuration here (or press Enter to skip):
```

- Input the host name where the registry will reside as https://onix-registry.becknprotocol.io

```
No network configuration URL provided, proceeding without it.

Enter publicly accessible registry URL: https://onix-registry.becknprotocol.io
```

- The installation will complete to indicate that the registry has been installed.

```
................Installing required packages................
Docker Bash completion is already installed.
docker-compose is already installed.
Package Installation is done
onix-registry.becknprotocol.io
................Installing Registry service................
WARN[0000] /home/ec2-user/beckn-onix/install/docker-compose-v2.yml: `version` is obsolete
[+] Running 1/1
 ✔ Container registry  Started                                                                 0.5s
Registry installation successful
[Installation Logs]
Your Registry setup is complete.
You can access your Registry at https://onix-registry.becknprotocol.io
Process complete. Thank you for using Beckn-ONIX!
```

## Install a gateway for the network

Please refer to the [Setting up a gateway](./user_guide.md/#setting-up-a-gateway) section of the user guide for the prerequisites and additional information.

- On the virtual server that will hold the gateway, clone the repo

```
git clone https://github.com/beckn/beckn-onix.git

```

- Due to a [known issue](https://github.com/beckn/beckn-onix/issues/8) with the new version of gateway, we need to do the following. This will be fixed by the project very soon and this step will not be required then. Open `beckn-onix/install/gateway_data/config/networks/onix.json` in an editor and change its contents to the following

```
{
    "core_version" : "1.1.0",
    "registry_id": "onix-registry.becknprotocol.io..LREG",
    "search_provider_id" : "onix-gateway.becknprotocol.io",
    "self_registration_supported": true,
    "subscription_needed_post_registration" : true,
    "base_url": "https://onix-registry.becknprotocol.io",
    "registry_url" : "https://onix-registry.becknprotocol.io/subscribers",
    "extension_package": "in.succinct.beckn.boc",
    "wild_card" : ""
}
```

- Change into the install folder and run the beckn-onix.sh script.

```
cd beckn-onix/install
./beckn-onix.sh

```

- In the prompt that comes up, choose joining an existing network.

```
Beckn-ONIX is a platform that helps you quickly launch and configure beckn-enabled networks.

What would you like to do?
1. Join an existing network
2. Create new production network
3. Set up a network on your local machine
4. Merge multiple networks
5. Configure Existing Network
(Press Ctrl+C to exit)
Enter your choice: 1

```

- Choose the component to install as Gateway

```
Which platform would you like to set up?
1. Gateway
2. BAP
3. BPP
Enter your choice: 1
```

- Skip the option to apply network configuration

```
Proceeding with the setup for Gateway...
Please provide the network-specific configuration URL.
Paste the URL of the network configuration here (or press Enter to skip):
```

- Input the URL of the registry we just now installed https://onix-registry.becknprotocol.io

```
No network configuration URL provided, proceeding without it.

Enter your registry URL: https://onix-registry.becknprotocol.io
```

- Input the Gateway URL https://onix-gateway.becknprotocol.io

```
Enter publicly accessible gateway URL: https://onix-gateway.becknprotocol.io
```

- The installation will complete to indicate the Gateway has been installed and registered with the registry

```
................Installing required packages................
Docker Bash completion is already installed.
docker-compose is already installed.
Package Installation is done
  % Total    % Received % Xferd  Average Speed   Time    Time     Time  Current
                                 Dload  Upload   Total   Spent    Left  Speed
100   555    0   533  100    22   3551    146 --:--:-- --:--:-- --:--:--  3724
Signing Public Key: LlT+DXNzpEKenZuBfhaRl4vvgRxAI2wm8O7/2vmsb0E=
Encryption Public Key: qhlWmkfy6WgzSSsGFc9dDfu3Sm3ZbbFf1bYiG+2RjFw=
URL https://onix-registry.becknprotocol.io/subscribers
................Installing Gateway service................
Creating gateway ... done
Registering Gateway in the registry
{
  "SWFHttpResponse" : {
    "Message" : ""
    ,"Status" : "OK"
  }
}
Gateway installation successful
[Installation Logs]
Your Gateway setup is complete.
You can access your Gateway at https://onix-gateway.becknprotocol.io
Process complete. Thank you for using Beckn-ONIX!
```

## Install a Beckn Adaptor for the BAP

- On the virtual server that will hold the BAP, clone the repo, change into the install folder and run the beckn-onix.sh script.

```
git clone https://github.com/beckn/beckn-onix.git
cd beckn-onix/install
./beckn-onix.sh

```

- In the prompt that comes up, choose joining an existing network.

```
What would you like to do?
1. Join an existing network
2. Create new production network
3. Set up a network on your local machine
4. Merge multiple networks
5. Configure Existing Network
(Press Ctrl+C to exit)
Enter your choice: 1
```

- Choose the component to install as BAP

```
Which platform would you like to set up?
1. Gateway
2. BAP
3. BPP
Enter your choice: 2
```

- Skip the option to apply network configuration

```
Proceeding with the setup for BAP...
Please provide the network-specific configuration URL.
Paste the URL of the network configuration here (or press Enter to skip):
```

- Input the BAP subscriber id - onix-bap.becknprotocol.io
- Input the BAP URL - https://onix-bap.becknprotocol.io
- Input the subscription endpoint of the registry - https://onix-registry.becknprotocol.io/subscribers

```
Enter BAP Subscriber ID: onix-bap.becknprotocol.io
Enter BAP Subscriber URL: https://onix-bap.becknprotocol.io
Enter the registry_url(e.g. https://registry.becknprotocol.io/subscribers)https://onix-registry.becknprotocol.io/subscribers
```

- The installation will complete to indicate the BAP Beckn Adaptor has installed.

```
................Installing required packages................
Docker Bash completion is already installed.
docker-compose is already installed.
Package Installation is done
................Installing MongoDB................
WARN[0000] /home/ubuntu/beckn-onix/install/docker-compose-app.yml: `version` is obsolete
[+] Running 1/1
 ✔ Container mongoDB  Started                                                                  0.4s
MongoDB installation successful
................Installing RabbitMQ................
WARN[0000] /home/ubuntu/beckn-onix/install/docker-compose-app.yml: `version` is obsolete
[+] Running 1/1
 ✔ Container rabbitmq  Started                                                                 0.5s
RabbitMQ installation successful
................Installing Redis................
WARN[0000] /home/ubuntu/beckn-onix/install/docker-compose-app.yml: `version` is obsolete
[+] Running 1/1
 ✔ Container redis  Started                                                                    0.6s
Redis installation successful
Generating public/private key pair
Your Private Key: o1t1TvdFaHU1H+2wDTsCEJgMRU9zdVt20SeFRyT0nyOlZujB4B0XZX1bMlchKBUpHQ65/9BCj6aMzS0Rdf+dRw==
Your Public Key: pWboweAdF2V9WzJXISgVKR0Ouf/QQo+mjM0tEXX/nUc=
Configuring BAP protocol server
Registering BAP protocol server on the registry
Network Participant Entry is created. Please login to registry https://onix-registry.becknprotocol.io/subscribers and subscribe you Network Participant.
WARN[0000] /home/ubuntu/beckn-onix/install/docker-compose-v2.yml: `version` is obsolete
[+] Running 1/1
 ✔ Container bap-client  Started                                                               0.4s
WARN[0000] /home/ubuntu/beckn-onix/install/docker-compose-v2.yml: `version` is obsolete
[+] Running 1/1
 ✔ Container bap-network  Started                                                              0.5s
Protocol server BAP installation successful
[Installation Logs]
Your BAP setup is complete.
You can access your BAP at https://onix-bap.becknprotocol.io
Process complete. Thank you for using Beckn-ONIX!

```

## Install a Beckn Adaptor for the BPP

- On the virtual server that will hold the BPP, clone the repo, change into the install folder and run the beckn-onix.sh script.

```
git clone https://github.com/beckn/beckn-onix.git
cd beckn-onix/install
./beckn-onix.sh

```

- In the prompt that comes up, choose joining an existing network.

```
What would you like to do?
1. Join an existing network
2. Create new production network
3. Set up a network on your local machine
4. Merge multiple networks
5. Configure Existing Network
(Press Ctrl+C to exit)
Enter your choice: 1

```

- Choose the component to install as BPP

```
Which platform would you like to set up?
1. Gateway
2. BAP
3. BPP
Enter your choice: 3
```

- Skip the option to apply network configuration

```
Proceeding with the setup for BPP...
Please provide the network-specific configuration URL.
Paste the URL of the network configuration here (or press Enter to skip):
```

- Input BPP subscriber id as onix-bpp.becknprotocol.io
- Input the BPP URL as https://onix-bpp.becknprotocol.io
- Input the registry URL to subscribe as https://onix-registry.becknprotocol.io/subscribers
- Input the webhook URL as the endpoint where your seller app or marketplace is. In case you do not have one, you can try 'https://unified-bpp.becknprotocol.io/beckn-bpp-adapter'. However the availability of a seller software for ever at this endpoint is not guaranteed (It currently is present)

```
Enter BPP Subscriber ID: onix-bpp.becknprotocol.io
Enter BPP Subscriber URL: https://onix-bpp.becknprotocol.io
Enter the registry_url(e.g. https://registry.becknprotocol.io/subscribers): https://onix-registry.becknprotocol.io/subscribers
Enter Webhook URL: https://unified-bpp.becknprotocol.io/beckn-bpp-adapter
```

- The installation will complete to indicate the BPP Beckn Adaptor has installed.

```
................Installing required packages................
Docker Bash completion is already installed.
docker-compose is already installed.
Package Installation is done
................Installing MongoDB................
WARN[0000] /home/ec2-user/beckn-onix/install/docker-compose-app.yml: `version` is obsolete
[+] Running 1/1
 ✔ Container mongoDB  Started                                                                  0.4s
MongoDB installation successful
................Installing RabbitMQ................
WARN[0000] /home/ec2-user/beckn-onix/install/docker-compose-app.yml: `version` is obsolete
[+] Running 1/1
 ✔ Container rabbitmq  Started                                                                 0.6s
RabbitMQ installation successful
................Installing Redis................
WARN[0000] /home/ec2-user/beckn-onix/install/docker-compose-app.yml: `version` is obsolete
[+] Running 1/1
 ✔ Container redis  Started                                                                    0.6s
Redis installation successful
................Installing Protocol Server for BPP................
Generating public/private key pair
Configuring BAP protocol server
Registering BPP protocol server on the registry
Network Participant Entry is created. Please login to registry https://onix-registry.becknprotocol.io/subscribers and subscribe you Network Participant.
WARN[0000] /home/ec2-user/beckn-onix/install/docker-compose-v2.yml: `version` is obsolete
[+] Running 1/1
 ✔ Container bpp-client  Started                                                               0.4s
WARN[0000] /home/ec2-user/beckn-onix/install/docker-compose-v2.yml: `version` is obsolete
[+] Running 1/1
 ✔ Container bpp-network  Started                                                              0.5s
Protocol server BPP installation successful
[Installation Logs]
Your BPP setup is complete.
You can access your BPP at https://onix-bpp.becknprotocol.io
Process complete. Thank you for using Beckn-ONIX!
```

## Change the status of the BAP and BPP on registry to Subscribed

The newly added BAP and BPP should be transitioned to the "SUBSCRIBED" state in the registry.

- Login to the newly installed registry (e.g. https://onix-registry.becknprotocol.io). The default username and password are root/root
- In the Admin menu, click Network Participant
- Click the pencil icon next to the onix-bap.becknprotocol.io
- Click on the Network Role tab
- Click on the pencil icon in the row of onix-bap.becknprotocol.io
- Change the status to SUBSCRIBED
- Click the Done button.

- In the Admin menu, click Network Participant
- Click the pencil icon next to the onix-bpp.becknprotocol.io
- Click on the Network Role tab
- Click on the pencil icon in the row of onix-bpp.becknprotocol.io
- Change the status to SUBSCRIBED
- Click the Done button.

## Update BAP and BPP with the layer 2 configuration files for the domains we are interested in

The installation so far has installed a core Beckn network with the registry, gateway, BAP and the BPP. We cannot perform tranasctions on it till we have a layer 2 config file installed for the domains we want to transact in.

- Login to the virtual server with the BAP
- Change into the beckn-onix/layer2 folder
- Run the download_layer_2_config_bap.sh file.
- Specify the path to the layer 2 config file for the domain of interest. For example, for retail, we have https://raw.githubusercontent.com/beckn/beckn-onix/main/layer2/samples/retail_1.1.0.yaml and for energy https://raw.githubusercontent.com/beckn/beckn-onix/main/layer2/samples/uei_charging_1.1.0.yaml

- Login to the virtual server with the BPP
- Change into the beckn-onix/layer2 folder
- Run the download_layer_2_config_bpp.sh file.
- Specify the path to the layer 2 config file for the domain of interest. For example, for retail, we have https://raw.githubusercontent.com/beckn/beckn-onix/main/layer2/samples/retail_1.1.0.yaml and for energy https://raw.githubusercontent.com/beckn/beckn-onix/main/layer2/samples/uei_charging_1.1.0.yaml

- Now with these layer 2 configs installed, we can conduct retail and energy transactions on the network.

## Conduct successful transactions on the network

- Load the collection available at `artifacts\ONIX Demo Collection.postman_collection.json` in this repo.
- Run the UEI >> Search request
- The request should succeed without any errors.
- Additional folders and tests will be addded to this collection.
