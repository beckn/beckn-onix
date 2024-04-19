# Creating a Beckn network for healthcare using Beckn-ONIX and transacting on it.

## Introduction

[Beckn](https://becknprotocol.io/) as a protocol allows creation of decentralised commerce network across many domains. Healthcare is one of them. Beckn-ONIX is a project aimed at easing setup and maintanence of Beckn network for different domains. It provides utilities to quickly setup the core Beckn network as well as provides tools to enable domain specific transactions. The latter is achieved through the use of layer 2 configuration files.

The [Beckn-ONIX User Guide](./user_guide.md) provides an overall understanding of setup of the core Beckn network as well as download and ingestion of Layer 2 configuration files. The [step by step setup](./setup_walkthrough.md) walks through one such installation in great detail. The layer 2 configurations illustrated in that demo belong to the retail and energy domain.

## Healthcare usecases on Beckn network

A sample of the usecases that can be imagined on Beckn networks include

- Discovery and request for ambulance service including scenarios such as
  - Requesting an immediate ambulance service
  - Scheduled pickup by ambulance
  - Querying special ambulances for certain category of patients
  - Requesting with both start and destination locations
- Blood bank related use cases including
  - Finding and transacting for emergency blood
  - Identifying and enrolling for donation
- Doctor consultation including
  - search by location
  - search by speciality
- Diagnostics related transactions including
- Pharmacy related transactions including
  - Location aware search

## Setting up Beckn network for healthcare usecases

Setting up of a network for healthcare related use cases follows the same steps as illustrated in the [step by step setup](./setup_walkthrough.md) document. The primary differences is the layer 2 configuration file downloaded will be specific to healthcare and suited to conduct healthcare transactions. A sample layer 2 config for healthcare is available in the beckn_onix/layer2/samples folder and its path can be provided when running download_layer_2_config_bap.sh and download_layer_2_config_bpp.sh. If you are joining a network administered by a network facilitator, they might have a different layer 2 config and that should be used instead of the sample.

The following is a conceptual view of core and layer 2 config from a perspective of validation
![Validation in Beckn Adaptor](./images/message_validation_in_ba.png)

## Writing Layer 2 configuration file for healthcare domain

Layer 2 configuration file addresses two primary concerns

- The rules and policies agreed by participants from the healtcare domain and agreed upon usually through working groups or other mechanisms
- The rules and policies required by the network facilitator.

At an implementation level, the layer 2 configuration has additional data over core in two primary areas

- Description, examples and other non-machine enforceable information. These are useful for implementors to get the big picture, perform semantic validation and use example requests as base for their own modifications.
- Rules and policies against which incoming messages can be validated. These are over and above those in the core and cover domain specific scenarios.

## Sample validations that can be encoded into layer 2 configuration

1. Limiting values to a fixed set of enumerations. Examples include

- Limiting fulfillment type to ["walk-in","doorstep service"] for diagnostics
- Limiting fulfillment type to ["IN-STORE_PICKUP","HOME-DELIVERY"] for items in pharmacy
- Limiting fulfillment type to ["EMERGENCY","NORMAL"] for blood bank related queries

```
type: object
properties:
  type:
    type: string
    enum:
      - EMERGENCY
      - NORMAL
required:
  - type
```

2. Limiting values using minimum and maximum

- Setting minimum and maximum of count field of unitized object to indicate a single unit item in medicine packages

```
unitized:
  description: This represents the quantity available in a single unit of the item
  type: object
  properties:
    count:
      type: integer
      minimum: 1
      maximum: 1
    measure:
      $ref: '#/components/schemas/Scalar'
```

3. Marking an optional field in the core as required in the layer 2 config

- Mandating feedback form be filled after consultation during rating
- Mandating cancellation reason for appointment cancellation
- Mandating credentials of doctor be present in search

```
type: object
properties:
  cancellation_reason:
    type: string
required:
  - cancellation_reason
```

4. Mandating custom tags to be present in the tag list

- Requiring the symptoms field be filled before the doctor's appointment is confirmed
- Requiring the qualifications of doctor to be returned ih search results in either skills or verifiable creds

```
type: object
properties:
  creds:
    type: object
  skills:
    type: object
oneOf:
- required:
  - skills
- required:
  - creds
```

5. Conditional requirement based on value of a different field

- If item is medical equipment, additinoal description should be present

```
type: object
properties:
  code:
    type: string
  additional_desc:
    type: string
anyOf:
- not:
    properties:
      code:
        const: "medical-equipment"
    required:
    - code
- required:
  - additional_desc
```

## Conclusion

Multitide DHP related use cases can be implemented on the Beckn networks. The process Beckn-ONIX follows to create core Beckn network and allowing participants to download layer 2 configuration is identical across domains. Layer 2 configuration captures both domain policies and rules as well as those enforced by the network facilitators. Layer 2 configuration are both prescriptive as well as mandatory. The mandatory rules and policies can be written on top of the core spec within the layer 2 config and can be enforced by incorporating a validator in the workflow.
