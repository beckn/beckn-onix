# ONIX Setup Script

## Overview

This shell script, `onix.sh`, automates the setup of Beckn components, including the Registry, Gateway, Protocol Server BAP, Protocol Server BPP, Sandbox, Webhook, and supporting services such as MongoDB, Redis, and RabbitMQ.

## How to Use

### Prerequisites

1. Git
2. Docker

## Installation

### Debian / Ubuntu

**Clonning Onix Repo**

```
git clone https://github.com/beckn/beckn-onix.git
```

**Installing Docker**

```
sudo apt install docker
```

**Running Onix**

```
cd beckn-onix/ && cd  install/
```

```
./onix.sh
```

Note: when running this file may lead to errors. The easy way to solve is to run the `onix.sh` file with root permissions.

```
sudo ./onix.sh
```

The reason for this error is due to the current user not having all the permission to access certain files present in the system.

**Domain**

You will also be requiring a domain and also need to map all the 4 server with each sub domain for example

```
registry.example.io
gateway.example.io
bpp.example.io
bap.example.io
```
## Registry

**Default Registry Username and Password**
```
username = root
password = root
```

The network configuration URL is not mandatory as this is for extending the core capabilities of the network
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
