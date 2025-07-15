# Beckn Adapter Layer 2 Configuration

## Overview of Layer 2 Configuration Files

The **Beckn-ONIX** Beckn Adapter (also known as the protocol server for **BAP**/**BPP**) requires **Layer 2 configuration files**. These files are needed for the domain name (`domain` field) and core specification version (`version` field) as defined in the message context. The Layer 2 configuration files are written in **YAML** format and contain **domain-specific** and **network-specific rules**. The Beckn Adapter expects these files to be present in its file system with the naming convention `{domain}_{version}.yaml`, accessible within the pod.

## Using Amazon EFS to Store Layer 2 Config Files

Because both BAP and BPP services may run multiple pods in a production-like environment, it is ideal to use a **shared volume** such as **Amazon EFS** to store the Layer 2 configuration files. This allows all relevant pods to access these configuration files seamlessly. 

Upon startup, BAP and BPP pods mount their respective **EFS volumes**, and the Layer 2 configuration files (which are baked into the protocol server container image) are copied over to the EFS volume. This ensures that the configuration files are shared and available across pods.

## Uploading Layer 2 Configuration Files

To facilitate the use of Layer 2 configuration files across multiple pods, **Amazon EFS** is employed. The EFS volume can be mounted onto an **EC2 instance** (a micro instance is sufficient) where you can upload the Layer 2 configuration files. These files will then be immediately available to the corresponding pods through their mounted volumes.

### Steps to Mount an Amazon EFS Volume

#### 1. Install the `amazon-efs-utils` package

First, install the **amazon-efs-utils** package on your EC2 instance to enable EFS mounting. Additionally, you may need to use SCP to copy the new Layer 2 configuration files to the EC2 instance.

#### 2. Retrieve the File System ID

You can find the **fileSystemId** either in the **EFS service** (created by the Helm chart during BAP/BPP service deployment) or by querying the Kubernetes storage class. Use the following commands to retrieve the **fileSystemId**:

```bash
# For BAP:
kubectl get sc beckn-onix-bap-efs-storageclass -o jsonpath='{.parameters.fileSystemId}'

# For BPP:
kubectl get sc beckn-onix-bpp-efs-storageclass -o jsonpath='{.parameters.fileSystemId}'
```

#### 3. Mount the EFS Volume

Once you have the fileSystemId, you can mount the file system to the EC2 instance. Follow these steps:

```bash
$ mkdir efs
$ sudo mount -t efs file-system-id:/ efs/
```

#### 4. Upload the Layer 2 Configuration Files
After mounting the EFS file system, you can copy the Layer 2 configuration files to the mounted directory:

```bash
$ cp /path/to/layer2/configuration/files/* efs/
```