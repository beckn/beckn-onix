# Creating a new Beckn network using ONIX and conducting layer 2 transactions

## Introduction

This document describes the process to create a new Beckn network and conduct Energy transactions on it. Please refer to the [User Guide](.//user_guide.md) for details on the pre-requesites and additional details on all the steps.

The [Demo Walkthrough](./demo_walkthrough.md) document describes how to install a new Beckn network. In general, readying a new network for installation involves the following steps

1. Installing the core network with the registry, gateway, BAP adaptor and the BPP adaptor.
2. Installing the layer 2 config files for the domains in which we want to transact
3. Conducting successful transactions on the new network.

The [Demo Walkthrough](./demo_walkthrough.md) illustrates download of retail domain layer 2 configuration. For energy domain all the steps remain the same except that we will have to download the layer 2 config file for energy (e.g. https://raw.githubusercontent.com/beckn/beckn-onix/main/layer2/samples/uei_charging_1.1.0.yaml)

In this document we describe how we can create a energy domain layer 2 config file such as the example above. While these instructions are similar for other domains, the values used in example are more suited for energy domain.

## Creating a layer 2 config file for energy domain

We start with either the core specification or a working group recommended layer 2 config for the domain as the base. Some of the changes we do to this base spec include

### Adding enumerations for fields

### Marking fields as required

### Creating tags

### Adding tags and requirements on those as conditionals

## Additional steps

After all the modifications to the layer 2 config are made for the domain, it needs to be hosted in a centralized place, so the network participants can download them as illustrated in the [Demo Walkthrough](./demo_walkthrough.md). Once these are downloaded by the BAP and the BPP, they can conduct transactions in the domain.
