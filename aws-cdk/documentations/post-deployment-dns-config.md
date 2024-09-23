# Beckn-ONIX DNS Configuration

After verifying that the Beckn-Onix services (`registry`, `gateway`, `bap-network`, and `bap-client`) are successfully deployed, you need to update your DNS settings to ensure proper routing of traffic. Follow these steps to configure your DNS records.

### Retrieve the Amazon ALB's DNS Addresses
Run following commands to extract the external DNS name of the Amazon ALB attached with Ingress across all Beckn-ONIX services. 

Alternatively, you can retrieve the DNS names of the Amazon ALBs associated with the Ingress resources from the AWS Management Console or using the AWS CLI.

#### Registry

   ```bash
   kubectl -n beckn-onix-registry get ingress -o jsonpath='{.items[*].status.loadBalancer.ingress[*].hostname}'
   ```

#### Gateway
   ```bash
   kubectl -n beckn-onix-registry get ingress -o jsonpath='{.items[*].status.loadBalancer.ingress[*].hostname}'
   ```

#### BAP Network
   ```bash
   kubectl -n beckn-onix-bap get ingress -o jsonpath='{.items[*].status.loadBalancer.ingress[*].hostname}'
   ```

#### BPP Network
   ```bash
   kubectl -n beckn-onix-bpp get ingress -o jsonpath='{.items[*].status.loadBalancer.ingress[*].hostname}'
   ```

### Update DNS Records

#### 1. Log in to Your DNS Provider

Access the management console of your domain registrar or DNS hosting provider. For instance, if using Amazon Route 53, go to the Route 53 dashboard in the AWS Management Console.

#### 2. Add DNS Records

Create or update DNS records for each service. You need to set up the following DNS records for your services:

- **Type:** CNAME (or Alias record if using Route 53)
- **Name:** The subdomain you want to use (e.g., `registry.beckn-onix-aws-cdk.becknprotocol.io`, `gateway.beckn-onix-aws-cdk.becknprotocol.io`, etc.)
- **Value:** The respective DNS name of the Amazon ALB retrieved in the previous step.

## Next Steps

After updating your DNS records, you need to register your participants BAP and BPP network with the registry service. Follow the steps in the [BAP and BPP Registration](documentations/post-deployment-bap-bpp-register.md) document to complete this process.

**[Register BAP and BPP with Registry](documentations/post-deployment-bap-bpp-register.md)**

