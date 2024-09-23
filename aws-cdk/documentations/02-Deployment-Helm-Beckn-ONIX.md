# Beckn-ONIX AWS CDK Helm Charts

This repository contains Helm charts for deploying the Beckn-ONIX services on AWS using the AWS CDK framework. The charts are designed to deploy the following applications:

- **Registry**: Manages Beckn service providers and categories, and provides an additional layer of trust on the network by listing platforms that are compliant to a network’s rules and policies.
- **Gateway**: Central point for routing Beckn messages between providers and participants.
- **BAP (Beckn Application Platform)**: A consumer-facing infrastructure which captures consumers’ requests via its UI applications, converts them into beckn-compliant schemas and APIs at the server side, and fires them at the network.
- **BPP (Beckn Provider Platform)**: Other side of the network is the supply side which consists of Beckn Provider Platforms (BPPs) that maintain an active inventory, one or more catalogs of products and services, implement the supply logic and enable fulfillment of orders.

## Prerequisites

- **Amazon EKS Requirements**:
  - [**Load Balancer Controller**](https://docs.aws.amazon.com/eks/latest/userguide/aws-load-balancer-controller.html): Required for **Registry** and **Gateway**.
  - [**EBS CSI Driver**](https://docs.aws.amazon.com/eks/latest/userguide/pv-csi.html) and [**EFS CSI Driver**](https://docs.aws.amazon.com/eks/latest/userguide/efs-csi.html): Required for **BAP** and **BPP**.
  
  If deploying all Beckn-ONIX components on the same EKS cluster, all three add-ons are necessary.

- **Kubectl Client**: Configured with the Amazon EKS cluster.
- **Helm 3 Client**: For managing Helm charts.
- **A PostgreSQL Database Instance**: Managed by AWS RDS Aurora in this case.
- **Public Domain/Sub-Domain**: Along with SSL certificates for HTTPS.


### Domain and Subdomains

Beckn-ONIX requires a public domain to be associated with the following services:

- Registry
- Gateway
- BAP Network
- BPP Network

Users must obtain a public domain and create subdomains for each service. Additionally, an SSL certificate must be issued for each subdomain to enable HTTPS. You can use [AWS Certificate Manager](https://aws.amazon.com/certificate-manager/pricing/), which provides public SSL/TLS certificates at no cost.

## Requesting a Public SSL Certificate through AWS Certificate Manager

Gather the list of subdomains you intend to use for Beckn-ONIX services (as outlined in the pre-requisite).

To obtain an SSL certificate through AWS Certificate Manager, follow the easy steps provided in the official [AWS ACM Documentation](https://docs.aws.amazon.com/acm/latest/userguide/gs-acm-request-public.html).

Once a certificate is issued, copy the certificate ARN to be used in the Helm charts later. The certificate ARN follows this format:

`arn:aws:acm:ap-south-1:<aws-account-id>:certificate/<identifier>`

## Helm Parameters
Before installing the Helm chart, it’s important to familiarize yourself with all the available parameters. Each parameter allows you to customize the Helm chart according to your deployment needs. Review the descriptions and default values to understand how they will impact your setup.

**Note:** If a parameter does not have a default value listed, you are expected to provide a value for it during Helm installation.

### Registry Parameters

**Note:** Default values that are empty must be provided during chart execution.

| Name                          | Description                             | Default Value                                                |
| ----------------------------- | --------------------------------------- | ---------------------------------------------------- |
| `externalDomain`               | External domain for the Registry service, e.g. <br> `registry.beckn-onix-aws-cdk.becknprotocol.io`|           |
| `database.host`                | PostgreSQL database host, e.g. <br> `beckn-onix-registry.ap-south-1.rds.amazonaws.com`|          |
| `database.dbname`              | PostgreSQL database name                 | `registry`                                            |
| `database.username`            | PostgreSQL database username             | `postgres`                                            |
| `database.password`            | PostgreSQL database password             |                                         |
| `ingress.tls.certificateArn`   | ARN for the TLS certificate, e.g. <br> `arn:aws:acm:region:account-id:certificate/certificate-id`|            |

---

### Gateway Parameters

**Note:** Default values that are empty must be provided during chart execution.

| Name                          | Description                             | Default Value                                                |
| ----------------------------- | --------------------------------------- | ---------------------------------------------------- |
| `externalDomain`               | External domain for the Gateway service, e.g. <br> `gateway.beckn-onix-aws-cdk.becknprotocol.io`|         |
| `registry_url`                 | Registry URL for Beckn services, e.g.  <br> `https://registry.beckn-onix-aws-cdk.becknprotocol.io`|         |
| `database.host`                | PostgreSQL database host, e.g. <br> `beckn-onix-registry.ap-south-1.rds.amazonaws.com`|        |
| `database.dbname`              | PostgreSQL database name                 | `gateway`                                             |
| `database.username`            | PostgreSQL database username             | `postgres`                                            |
| `database.password`            | PostgreSQL database password             |                                        |
| `ingress.tls.certificateArn`   | ARN for the TLS certificate, e.g. <br> `arn:aws:acm:region:account-id:certificate/certificate-id`|           |

---

### BAP/BPP Parameters

**Note:** Default values that are empty must be provided during chart execution.

| Name                                      | Description                                        | Default Value                                               |
| ----------------------------------------- | -------------------------------------------------- | --------------------------------------------------- |
| `global.externalDomain`                   | External domain for the BAP/BPP network service, e.g. `bap-network.beckn-onix-aws-cdk.becknprotocol.io` (BAP), `bpp-network.beckn-onix-aws-cdk.becknprotocol.io` (BPP)|           |
| `global.registry_url`                     | Registry URL for Beckn services, e.g. `https://registry.beckn-onix-aws-cdk.becknprotocol.io`|                         |
| `global.responseCacheMongo.username`      | MongoDB username for response caching              | `root`                                              |
| `global.responseCacheMongo.password`      | MongoDB password for response caching              |
| `global.responseCacheMongo.host`      | MongoDB host for response caching              | `mongodb.bap-common-services.svc.cluster.local` | 
| `global.rabbitMQamqp.password`            | RabbitMQ AMQP password for message processing      |                                           |
| `global.rabbitMQamqp.host`            | RebbitMQ host | `rabbitmq.bap-common-services.svc.cluster.local` |
| `global.redisCache.host`            | Redis host | `redis-master.bap-common-services.svc.cluster.local ` |
| `global.ingress.tls.certificateArn`       | ARN for the TLS certificate, e.g. `arn:aws:acm:region:account-id:certificate/certificate-id`|             |
| `global.bap.privateKey` or `global.bpp.privateKey`       | Private key for BAP/BPP, used during registration |             |
| `global.bap.publicKey` or `global.bpp.publicKey`       | Public key for BAP/BPP, used during registration |             |


## Installing the Charts

Before installing the charts, ensure AWS RDS Aurora PostgreSQL database is running and accessible from your EKS cluster.

### Beckn-ONIX Registry

```bash
helm install registry . \
  --set externalDomain=<registry_external_domain> \
  --set database.host=<rds_postgres_database_hostname> \
  --set database.password=<db_password> \
  --set ingress.tls.certificateArn="aws_certificate_manager_arm"
```
### Beckn-ONIX Gateway

```bash
helm install gateway . \
  --set externalDomain=<gateway_external_domain> \
  --set registry_url=https://<registry_domain> \
  --set database.host=<rds_postgres_database_hostname> \
  --set database.password=<rds_postgres_db_password> \
  --set ingress.tls.certificateArn="aws_certificate_manager_arm"
```

### Common Services Charts for BAP & BPP

BAP and BPP services require Redis, MongoDB, and RabbitMQ. These services must be installed before deploying Beckn-ONIX. You can use Bitnami Helm charts for installation: [Bitnami Helm Charts](https://github.com/bitnami/charts/tree/main/bitnami/).

#### Install Common Services for BAP

#### Create Namespace and Add Bitnami Helm Repository

```bash
   kubectl create namespace bap-common-services
   helm repo add bitnami https://charts.bitnami.com/bitnami
```

#### Install Redis
```bash
helm install -n bap-common-services redis bitnami/redis \
--set auth.enabled=false \
--set replica.replicaCount=0 \
--set master.persistence.storageClass="gp2" 
```

#### Install MongoDB
```bash
helm install -n bap-common-services mongodb bitnami/mongodb \
--set persistence.storageClass="gp2"

# To get the Mongodb root password run:
kubectl get secret --namespace bap-common-services mongodb -o jsonpath="{.data.mongodb-root-password}" | base64 -d)
```

#### Install RabbitMQ
```
helm install -n bap-common-services rabbitmq bitnami/rabbitmq \
--set persistence.enabled=true \
--set persistence.storageClass="gp2" \
--set auth.username=beckn \
--set auth.password=$(openssl rand -base64 12)
```

#### Install Common Services for BPP
For BPP, follow the same installation steps as for BAP, but with modifications specific to the BPP K8s namespace:

1. **Create Namespace for BPP and Add Bitnami Helm Repository**

```bash
   kubectl create namespace bpp-common-services
   helm repo add bitnami https://charts.bitnami.com/bitnami
```
#### Install Redis
```bash
helm install -n bpp-common-services redis bitnami/redis \
--set auth.enabled=false \
--set replica.replicaCount=0 \
--set master.persistence.storageClass="gp2" 
```

#### Install MongoDB
```bash
helm install -n bpp-common-services mongodb bitnami/mongodb \
--set persistence.storageClass="gp2"

# To get the Mongodb root password run:
kubectl get secret --namespace bap-common-services mongodb -o jsonpath="{.data.mongodb-root-password}" | base64 -d)
```

#### Install RabbitMQ
```
helm install -n bpp-common-services rabbitmq bitnami/rabbitmq \
--set persistence.enabled=true \
--set persistence.storageClass="gp2" \
--set auth.username=beckn \
--set auth.password=$(openssl rand -base64 12)
```

### Proceed to Install Beckn-ONIX BAP & BPP

#### Generate SSL Key Pair
The Protocol Server (BAP/BPP) provides a key generation script.

**Note:** Ensure Node.js is installed on your system.

```bash
curl https://raw.githubusercontent.com/beckn/protocol-server/master/scripts/generate-keys.js > generate-keys.js
npm install libsodium-wrappers
node generate-keys.js
```

Copy the `publicKey` and `privateKey` from the output. You need to pass keys to following Helm install command. These keys are also added into the K8s secrets via Helm chart. 

> **Info:** AWS CDK automates this process by using the same key generation script and passing the keys directly to the Helm chart.

#### Beck-ONIX BAP

```bash
helm install beckn-onix-bap . \
  --set global.externalDomain=<bap_network_external_domain> \
  --set global.registry_url=https://<registry_domain> \
  --set global.ingress.tls.certificateArn="aws_certificate_manager_arm" \
  --set global.bap.privateKey="private-key" \
  --set global.bap.publicKey="public-key" \ 
  --set global.efs.fileSystemId="efs-systemId"
```

#### Beckn-ONIX BPP

```bash
helm install beckn-onix-bpp . \
  --set global.externalDomain=<bpp_network_external_domain> \
  --set global.registry_url=https://<registry_domain> \
  --set global.ingress.tls.certificateArn="aws_certificate_manager_arm"
  --set global.bpp.privateKey="private-key" \
  --set global.bpp.publicKey="public-key" \
  --set global.efs.fileSystemId="efs-systemId"
```

## Next Steps

After installing all Beckn-Onix services, proceed with the next steps to complete the setup:

1. **[Verify Deployments](documentations/verify-deployments.md)**

   To ensure that your Beckn-Onix services are running correctly, follow the instructions in the [Verify Deployments](documentations/verify-deployments.md) document. This will help you confirm that the services are operational and identify any issues that need to be addressed.

2. **[Update DNS Records](documentations/post-deployment-dns-config.md)**

   To configure DNS settings for your services, follow the instructions provided in the [Post-Deployment DNS Configuration](documentations/post-deployment-dns-config.md) document. This will guide you through retrieving the necessary Load Balancer addresses and updating your DNS records.

3. **[Register BAP and BPP with Registry](documentations/post-deployment-bap-bpp-register.md)**

   After updating your DNS records, you need to register your participants BAP and BPP network with the registry service. Follow the steps in the [BAP and BPP Registration](documentations/post-deployment-bap-bpp-register.md) document to complete this process.

Make sure to follow the detailed steps in the linked documents to complete the setup and ensure your services are correctly configured and registered.
