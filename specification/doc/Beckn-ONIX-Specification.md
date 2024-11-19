<!-- tocstop -->
# Beckn-ONIX Software Specification: v0.1

## Table of Contents

- [Preface](#preface)
  - [Purpose of the document](#purpose-of-the-document)
  - [Prerequisites](#prerequisites)
  - [Audience](#audience)
  - [Disclaimers](#disclaimers)
  - [Definitions and Acronyms](#definitions-and-acronyms)
  - [Learning Resources](#learning-resources)
- [Introduction](#introduction)
  - [The world before Beckn](#the-world-before-beckn)
  - [Can we reimagine transactions on the internet?](#can-we-reimagine-transactions-on-the-internet)
  - [Why do we need Beckn-ONIX?](#why-do-we-need-beckn-onix)
  - [Objectives and Goals](#objectives-and-goals)
  - [Key Features](#key-features)
- [Requirements](#requirements)
  - [Functional Requirements](#functional-requirements)
    - [Requirements for all modes](#requirements-for-all-modes)
      - [Installation](#installation)
      - [Identity](#identity)
      - [Authentication - Key-Pair](#authentication---key-pair)
      - [Authentication - Signing](#authentication---signing)
      - [Authentication - Verification](#authentication---verification)
      - [Security-https](#security-https)
      - [Protocol Version compatibility](#protocol-version-compatibility)
      - [Layer 2 Config](#layer-2-config)
      - [Observability](#observability)
      - [Logging](#logging)
      - [Configuration](#configuration)
    - [Requirements for BAP mode](#requirements-for-bap-mode)
      - [Common Requirements](#common-requirements)
      - [Authentication - Key verification](#authentication---key-verification)
      - [DOFP functionality](#dofp-functionality)
      - [Sending Request (Transaction API)](#sending-request-transaction-api)
      - [Layer 2 Configuration (Transaction API)](#layer-2-configuration-transaction-api)
      - [Core spec compatibility  (Transaction API)](#core-spec-compatibility--transaction-api)
      - [Handling synchronous response  (Transaction API)](#handling-synchronous-response--transaction-api)
      - [Listening to responses  (Transaction API)](#listening-to-responses--transaction-api)
      - [Acknowledging responses (Transaction API)](#acknowledging-responses-transaction-api)
      - [Client side interface (Transaction API)](#client-side-interface-transaction-api)
      - [Context  (Transaction API)](#context--transaction-api)
      - [Error handling (Transaction API)](#error-handling-transaction-api)
      - [XInput (Transaction API)](#xinput-transaction-api)
      - [Meta API - Fetching information](#meta-api---fetching-information)
    - [Requirements for BPP Mode](#requirements-for-bpp-mode)
      - [Common Requirements](#common-requirements-1)
      - [Authentication - Key verification](#authentication---key-verification-1)
      - [Authentication - Verification - Gateway](#authentication---verification---gateway)
      - [DOFP functionality](#dofp-functionality-1)
      - [Listening to Requests (Transaction API)](#listening-to-requests-transaction-api)
      - [Layer 2 Configuration (Transaction API)](#layer-2-configuration-transaction-api-1)
      - [Core spec compatibility (Transaction API)](#core-spec-compatibility-transaction-api)
      - [Acknowledging request (Transaction API)](#acknowledging-request-transaction-api)
      - [Sending Responses (Transaction API)](#sending-responses-transaction-api)
      - [Handling response acknowledgements (Transaction API)](#handling-response-acknowledgements-transaction-api)
      - [Context (Transaction API)](#context-transaction-api)
      - [Responses without corresponding requests](#responses-without-corresponding-requests)
      - [XInput (Transaction API)](#xinput-transaction-api-1)
      - [Error handling (Transaction API)](#error-handling-transaction-api-1)
      - [Meta API - Listening and returning data](#meta-api---listening-and-returning-data)
    - [Requirements for Registry mode](#requirements-for-registry-mode)
      - [Common Requirements](#common-requirements-2)
      - [Registration of Network Participants](#registration-of-network-participants)
      - [Subscription of Network Participants](#subscription-of-network-participants)
      - [Network Participant - Keys](#network-participant---keys)
      - [Subscribe (Registry API)](#subscribe-registry-api)
      - [Lookup (Registry API)](#lookup-registry-api)
      - [Logging - Public key changes](#logging---public-key-changes)
      - [Merging two networks](#merging-two-networks)
    - [Requirements for Gateway mode](#requirements-for-gateway-mode)
      - [Common Requirements](#common-requirements-3)
      - [Authentication - Key verification](#authentication---key-verification-2)
      - [Authentication - Signing](#authentication---signing-1)
      - [Listening to search requests (Transaction API)](#listening-to-search-requests-transaction-api)
      - [Forwarding search (Transaction API)](#forwarding-search-transaction-api)
      - [Caching (Transaction API)](#caching-transaction-api)
  - [Non-Functional Requirements](#non-functional-requirements)
    - [Performance](#performance)
    - [Scalability](#scalability)
    - [Reliability and Availability](#reliability-and-availability)
    - [Usability](#usability)
- [Appendices](#appendices)
  - [APIs and Schemas](#apis-and-schemas)
  - [References](#references)


<!-- toc --> 

# Preface


## Purpose of the document

The purpose of this document is to define the software standard for Beckn-ONIX. By defining common interfaces and components, this document aims to ensure that all Beckn-ONIX distributions and SDKs are interoperable, enabling seamless integration and communication between systems. This standardization is crucial for enhancing consistency and reliability.


## Prerequisites

A good understanding of the Beckn Protocol and the ecosystem is required to get maximum benefit from this document. Learning resources are provided in the section below to help in this task.


## Audience

The users of this document are the developers or organizations who want to build a Beckn-ONIX distribution or a Beckn-ONIX SDK.


## Disclaimers
The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in [BECKN-010-Keyword-Definitions-for-Technical-Specifications](https://github.com/beckn/protocol-specifications/blob/keyword-defs/docs/BECKN-010-Keyword-Definitions-for-Technical-Specifications.md)

## Definitions and Acronyms

This section provides definitions and explanations of key terms and concepts used in the Beckn-Onix network. Understanding these terms is essential for navigating the functionalities and interactions within the ecosystem.


<table>
  <tr>
   <td><strong>Term</strong>
   </td>
   <td><strong>Description</strong>
   </td>
  </tr>
  <tr>
   <td>DOFP Session
   </td>
   <td>An end-to-end transaction workflow consisting of <strong>D</strong>iscovery, <strong>O</strong>rdering, <strong>F</strong>ulfillment, and <strong>P</strong>ost-fulfillment (not necessarily in the same sequence), resulting in the execution of a contract between a consumer and a provider. 
   </td>
  </tr>
  <tr>
   <td>BAP
   </td>
   <td>Beckn Application Platform
   </td>
  </tr>
  <tr>
   <td>BPP
   </td>
   <td>Beckn Provider Platform.
   </td>
  </tr>
  <tr>
   <td>BG
   </td>
   <td>Beckn Gateway.
   </td>
  </tr>
  <tr>
   <td>Registry
   </td>
   <td>Beckn Registry.
   </td>
  </tr>
  <tr>
   <td>Specification
   </td>
   <td>A technical document that describes how a system is to be developed to achieve a specific set of objectives or requirements.
   </td>
  </tr>
  <tr>
   <td>Ecosystem
   </td>
   <td>An ecosystem is a community of stakeholders that support platforms implementing the specification. Ecosystems can be made up of developers, resellers, customers, software, services, and more.
   </td>
  </tr>
  <tr>
   <td>Ecosystem Maturity
   </td>
   <td>The level of readiness of an ecosystem to design, build, and use implementations of the specification. This can range from basic access to internet infrastructure to more advanced features like digital identities, assets, payments, and trust infrastructure.
   </td>
  </tr>
  <tr>
   <td>Consumer
   </td>
   <td>A person, or an organization that wants to discover, buy or avail products, services, and/or opportunities.
   </td>
  </tr>
  <tr>
   <td>Provider
   </td>
   <td>A person, or an organization that wants to publish, sell, allocate, or provide products, services, and/or opportunities.
   </td>
  </tr>
  <tr>
   <td>Intent
   </td>
   <td>A consumer’s desire to purchase, procure, or avail something being offered by a provider.
   </td>
  </tr>
  <tr>
   <td>Catalog
   </td>
   <td>A provider’s list of products, services, and opportunities that is made available to consumers.
   </td>
  </tr>
</table>



## Learning Resources

Refer to the below resources to get an understanding of the Beckn protocol and Beckn-ONIX. Also refer to the references section of this document for additional resources.



* Beckn Protocol:
    1. [https://developers.becknprotocol.io/](https://developers.becknprotocol.io/)
    2. [https://developers.becknprotocol.io/docs/introduction/video-overview/](https://developers.becknprotocol.io/docs/introduction/video-overview/) 
* Beckn-Onix:
    1. [https://becknprotocol.io/beckn-onix/](https://becknprotocol.io/beckn-onix/)
    2. [https://github.com/beckn/beckn-onix/blob/main/docs/user_guide.md](https://github.com/beckn/beckn-onix/blob/main/docs/user_guide.md)


# Introduction

Beckn-ONIX envisions becoming the foundational standard for rapidly deploying and integrating Beckn protocol-enabled networks. By offering a robust, secure, and high-performance framework, Beckn-ONIX aims to empower the community to build adaptable, production-ready applications with ease. Much like Apache or Nginx serves as a backbone for HTTP-based systems, Beckn-ONIX aspires to be the go-to standard for creating reliable and scalable Beckn protocol adapters. This vision focuses on accelerating innovation by simplifying network deployment, enabling developers to focus on building the future of decentralized applications.


## Why do we need Beckn-ONIX?
* Automation
* Value Adjacency
* Future-proofing

> Note: to be expanded further

## Key Digital Functionalities

Any Beckn-ONIX distribution MUST offer the following functionalities:

* Setting up open networks
* Joining existing networks
* Configuring open networks


# Cross Cutting / Non-functional Requirements

This section lists the core requirements of a Beckn-ONIX distribution (sometimes referred to as **software**). It does not assume any implementation decisions except as required by the Beckn protocol (e.g. Beckn protocol messages are sent over http. So the network participant needs to expose http endpoints in order to receive them). Except those mentioned, all other design aspects can be chosen by the implementers.


## Functional Requirements

Beckn network defines Network Participants with four main roles. They are


* **BAP (Beckn Application Platform) **- consumer-facing infrastructure which captures consumers’ requests via its UI applications, converts them into beckn-compliant schemas and APIs at the server side, and fires them at the network.
* **BPP (Beckn Provider Platform) **- platforms that maintain an active inventory, one or more catalogs of products and services, implement the supply logic and enable fulfillment of orders. The BPP can be a single provider with a Beckn API implementation or an aggregator of merchants
* **Registry** - stores detailed information about every network participant and acts as the trust layer of the Beckn network.
* **Gateway** - are extremely lean and stateless routing servers. Its purpose is to optimize discovery of BPPs by the BAPs by merely matching the context of the search.

A Beckn-ONIX instance can take on any of the four roles of Network participants. We use the term **mode** in the rest of the document. We describe below (in different sub-sections), requirements common to all modes as well as requirements when operating in one of the modes. If an instance is operating in more than one mode, it should satisfy the requirements of all of those modes it is operating in. 

These **minimal core requirements** do not explain the business logic, application programming interface (except those on Beckn network side defined by the protocol) or user interface that might be required in any consumer side or provider side application. Some of these requirements will be covered in other Beckn-ONIX extended requirement documents. 

In the rest of the document, the word **Network Participant** refers to the Beckn-ONIX installed software instance that is acting as a Network Participant in the Beckn network. So when we say “Network Participant must…”, it effectively means that the “Beckn-ONIX distribution installed software instance must…”


### Requirements for all modes


#### Installation

1. It must be possible to install the software and configure it to operate in any mode required. The installation must allow 
    1. Creation of a new Beckn network (mostly relevant for the Registry mode)
    2. Joining an existing Beckn network as a network participant
    3. Configuring an existing network participant
    4. Merging two networks (relevant in the Registry mode)
2. The installation process should guide the user with intuitive prompts and help setup mandatory configuration such as identity (see below) . The exact process of installation and configuration (whether through wizards, GUI or combination of these explained through a procedure) is left to implementation.


#### Identity

1. Software must allow setting Subscriber Id and Subscriber URL of the network participant. This Subscriber Id and Subscriber URL must follow the format defined[ here.](https://github.com/beckn/protocol-specifications/blob/master/schema/Subscriber.yaml) This Subscriber Id and Subscriber URL must be used in messages to identify the network participant. Depending on the role and the message, different names (subscriber_id, bap_id, bpp_id ) are used to refer to these values. Network participant’s Subscriber Id and Subscriber URL must be registered with the Registry. While registering usually, the role (BAP, BPP, BG), the domain in which they transact and the location of the network participant is also set. The exact process(manual/automated) of registration may be defined by registry implementation.


#### Authentication - Key-Pair



1. It must be possible to set private-public key pairs of the Network Participant. The key pairs are associated with a key_id. The format of the key pairs, key_id and other details are documented[ here](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-006-Signing-Beckn-APIs-In-HTTP-Draft-01.md). Software may support multiple key-pairs with different key ids.
2. **Key-Pair generation**: The software should support easy generation of private-public key pairs.
3. **Public key update:** The public key of the key-pair must be updated at the registry. This is done using the subscribe endpoint and is documented[ here](https://github.com/beckn/protocol-specifications/blob/master/api/registry/build/registry.yaml).
4. **Security - Key-Pair refresh**: It must be possible for public-private key pair to be updated. The update will involve changing it at the registry using the subscribe endpoint as well as use of the new key in further transactions.


#### Authentication - Signing



1. Any message that is sent on the Beckn network must be hashed and signed with the private key associated with the network participant. This signature must be set as the Authorization header. The process will require subscriber_id, key_id, private key and the message body. The hashing and signing process as well as the format of the Authorization header is documented[ here](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-006-Signing-Beckn-APIs-In-HTTP-Draft-01.md).


#### Authentication - Verification



1. Any network participant when it receives a message from another participant must verify the signature to ensure that this is coming from the said participant and the message contents have not been modified. This process involves looking up the public key of the sender in the registry. The lookup endpoint is documented[ here](https://github.com/beckn/protocol-specifications/blob/master/api/registry/build/registry.yaml). The process of verification is documented[ here](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-006-Signing-Beckn-APIs-In-HTTP-Draft-01.md).
2. **Authentication - Verification - Lookup cache**: To reduce the load on the Registry, the network participant should use cache with a short expiry for the lookup api results. If the details of the required network participant are in the cache, the registry should not be contacted.


#### Security-https



1. All network participants must use https for communication towards the Beckn network. This ensures encryption of data in transit.


#### Protocol Version compatibility



1. Network Participants should support multiple versions of the core protocol.


#### Layer 2 Config



1. Layer 2 Config are machine encoded rules that are released by the network. These rules are an addition to the API rules defined by the core Beckn transaction API  specification. Currently Layer 2 configurations are openapi compliant yaml format files similar to Core transaction API specification.  The network might release one layer 2 config file for every domain the network participant can transcat in. \
 \
The software must provide a way to download and save multiple Layer 2 config released by the network. These layer 2 configurations must be used to enforce network defined rules. For the BAP/BPP mode, this might be validating incoming and outgoing messages as per the rules.
2. **Layer 2 Config - Core version**: Layer 2 Config file selection (in cases that involve message validation) should be cognisant of the core version(the value of the version attribute in the context of any message). If there is an available layer 2 config (released by network and downloaded by Network Particpant as specified above), specific to the core version in the message, that file should be used for validation.


#### Observability



1. **Health check**: Network Participants must implement a health check endpoint (/health). When operational, the endpoint must return a 200 HTTP status code. Optionally a body with additional details might be returned as illustrated in this[ example](https://docs.spring.io/spring-boot/api/rest/actuator/health.html).
2. **Observability- Telemetry**: Network participants may implement mechanisms to monitor different aspects of execution from an observability standpoint. If implemented, it should follow the[ Opentelemetry convention](https://opentelemetry.io/).


#### Logging



1. All Beckn messages that are sent or received by the Network Participant must be logged. This is recommended for dispute resolution, auditing and troubleshooting. This log should be stored in permanent storage for duration as might be required for potential dispute resolutions at the business level.


#### Configuration



1. **Location**: Software must allow location to be set (Country - City). This value must be used when required in the various messages.
2. **Port**: Software should allow configuration of the port where it hosts its application server(endpoints). This allows flexibility in deployment.


### Requirements for BAP mode


#### Common Requirements



1. All the requirements under the[ ](https://fide-official.atlassian.net/wiki/spaces/BBB/pages/edit-v2/572653580#Requirements-for-all-network-participants)“Requirements for all modes” section apply to the BAP mode.


#### Authentication - Key verification



1. In addition to the Authentication requirements specified in the “Requirements for all modes” section, an on_subscribe endpoint must be made available for the registry to verify the public-key set there. The details are documented[ here](https://github.com/beckn/protocol-specifications/blob/master/api/registry/build/registry.yaml).


#### DOFP functionality



1. The software operating in the BAP mode must facilitate the consumer side of the commerce transaction workflow including all the four stages of Discovery, Order, Fulfillment and Post fulfillment as described [here](https://developers.becknprotocol.io/docs/introduction/beckn-protocol-specification/). The mandatory parts of these requirements are listed below and tagged as “Transaction API”.
2. The software should implement or facilitate integration with additional business requirements associated with the transaction relevant to the consumer. 


#### Sending Request (Transaction API)



1. In BAP mode the software must be able to send the following Transaction APIs (all POST messages) to the Beckn Network (by calling the below endpoints on the BPP or the Gateway (for search). These APIs are part of transaction workflow covering all the four stages of Discovery, Order, Fulfillment and Post fulfillment. The format of the outgoing request is detailed[ here](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml). The endpoints to call and the contents of the payload that need to be sent are:
    1. search - search for product or service
    2. select - send cart
    3. init - initialize draft order
    4. confirm - confirm order
    5. status - get active order
    6. track - track order
    7. cancel - cancel order
    8. update - update order
    9. rating - provide rating
    10. support - get support contact details


#### Layer 2 Configuration (Transaction API)



1. Software must ensure that outgoing requests and incoming responses (described below) are compliant with **Layer 2** **configuration** rules as required by the network. The Layer 2 Config file to be used must be decided based on the context of the message (e.g. using the domain and version fields). In case no Layer 2 config file is found for a message, the software must use the matching version of[ Beckn Core Transaction API Specification](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml) to validate the messages.


#### Core spec compatibility  (Transaction API)



1. The description of the content of the messages provided in the[ Beckn Core Transaction API Spec](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml) must be followed by the software. Some of these attributes which have significant functionality attached, are described below. The[ Beckn Core Spec](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml) should be referred to for authoritative descriptions on all attributes.


#### Handling synchronous response  (Transaction API)



1. Receivers of Beckn messages  will respond back to API calls with ACK(success) / NACK(failure) as described[ here](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-003-Beckn-Protocol-Communication-Draft-01.md). The software must handle these suitably with graceful degradation and appropriate handling for failure messages.


#### Listening to responses  (Transaction API)



1. The software in BAP mode must implement the following endpoints (all POST) at its subscriber_url. The formats of the incoming responses are documented[ here](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml). The endpoints and the content of the payloads are:
    1. on_search - catalog response to a search request
    2. on_select - quote for cart/order sent in select
    3. on_init - draft order with payment terms
    4. on_confirm - confirmation of order created
    5. on_status - status of requested order
    6. on_track - tracking details of requested order
    7. on_update - updated order
    8. on_cancel - canceled order
    9. on_rating - acknowledgement for rating
    10. on_support - requested support information


#### Acknowledging responses (Transaction API)



1. The software must validate the response with the appropriate Layer 2 config file and acknowledge the response with an ACK(success) or NACK(failure) as described[ here](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-003-Beckn-Protocol-Communication-Draft-01.md). Appropriate business logic may be triggered after this acknowledgement is sent to the sender.


#### Client side interface (Transaction API)



1. While the entire request-response process in Beckn is asynchronous, the software in BAP mode may wrap this up to provide a synchronous API to the caller.(optional)


#### Context  (Transaction API)



1. The software must refer to the[ Beckn Core Transaction API](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml) for the meaning of the context attributes and their usage. Some of attributes that have a major impact on functionality are listed here.
    1. **message_id**: The software should generate and use a new message_id for every new interaction. This message_id is guaranteed to be intact in all corresponding asynchronous responses.
    2. **transaction_id**: The software may use the transaction_id across messages (search, select… confirm) to act as a proxy for a user session. The transaction_id is guaranteed to be returned intact in all corresponding asynchronous responses.
    3. **bap_id**: The software in the BAP mode must fill its own subscriber_id here.
    4. **bap_uri**: The software in the BAP mode must fill its own subscriber_url here.
    5. **bpp_id**: When sending peer-to-peer messages (all Transaction API except search) as well as to send search message only to one BPP, target BPP subscriber id must be sent in this field.
    6. **bpp_uri**: When sending peer-to-peer messages (all Transaction API except search) as well as to send search message only to one BPP, target BPP subscriber URL must be sent in this field.
    7. **ttl**: This defines the amount of time the request is considered active to correlate responses. Any responses for this request after this ttl must be dropped.


#### Error handling (Transaction API)



1. In case of error during the processing of requests, the BPP will return back error responses. The format of the error messages is defined in the Beckn Core Transaction Specification. The code to be used is specified in[ Error codes for BPP](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-005-Error-Codes-Draft-01.md). These should be gracefully handled on the BAP.


#### XInput (Transaction API) 



1. When the BPP needs more information from the consumer, it may use the XInput facility to redirect the user to a BPP defined form. The software in BAP mode should respect this request and the implementation should be as described in[ The XInput Schema](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-007-The-XInput-Schema.md).


#### Meta API - Fetching information 



1. BPPs expose meta APIs to provide metadata that may be useful to the BAP in its user interaction. The BAP may call these meta APIs as required. These meta APIs are described[ here](https://github.com/beckn/protocol-specifications/blob/master/api/meta/build/meta.yaml)


### Requirements for BPP Mode


#### Common Requirements



1. All the requirements under the[ ](https://fide-official.atlassian.net/wiki/spaces/BBB/pages/edit-v2/572653580#Requirements-for-all-network-participants)“Requirements for all modes” section apply to the BPP mode.


#### Authentication - Key verification



1. In addition to the Authentication requirements specified in the “Requirements for all modes” section, an on_subscribe endpoint must be made available for the registry to verify the public-key set there. The details are documented[ here](https://github.com/beckn/protocol-specifications/blob/master/api/registry/build/registry.yaml).


#### Authentication - Verification - Gateway



1. In addition to the Authentication-Verification requirements specified in the “Requirements for all modes” section, when the software in BPP mode receives a search message from the Gateway, there is an additional Proxy verification that must be done to ensure the message is sent by a valid Gateway and is not tampered. This involves lookup of the Gateway public key and is documented[ here](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-006-Signing-Beckn-APIs-In-HTTP-Draft-01.md)


#### DOFP functionality



1. The software operating in the BPP mode must facilitate the provider side of the commerce transaction workflow including all the four stages of Discovery, Order, Fulfillment and Post fulfillment as described [here](https://developers.becknprotocol.io/docs/introduction/beckn-protocol-specification/). The mandatory parts of these requirements are listed below and tagged as “Transaction API”. 
2. The software should implement or facilitate integration with additional business requirements associated with the transaction relevant to the provider. 


#### Listening to Requests (Transaction API)



1. Software in BPP mode must implement the following endpoints (all POST) at the subscriber_url. These APIs are part of the transaction workflow covering all the four stages of Discovery, Order, Fulfillment and Post fulfillment. The format of the incoming requests is detailed[ here](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml). The endpoints and the contents of the payloads are:
    1. search - search intent for product or service
    2. select - user’s cart or built order
    3. init - draft order from user
    4. confirm - order confirmation from user
    5. status - request for order status
    6. track - request for tracking url for order
    7. cancel - request for order cancellation
    8. update - request for order update
    9. rating - user rating
    10. support - request for support contact details


#### Layer 2 Configuration (Transaction API)



1. Software must ensure that incoming requests and outgoing responses (described below) are compliant with **Layer 2** **configuration** rules as required by the network. The Layer 2 Config file to be used must be decided based on the context of the message (e.g. using the domain and version fields). In case no Layer 2 config file is found for a message, the software must use the matching core version of[ Beckn Core Transaction API Specification](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml) to validate the messages.


#### Core spec compatibility (Transaction API)



1. The description of the content of the messages provided in the[ Beckn Core Transaction API Spec](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml) should be followed by the software. Some of these attributes which have significant functionality attached, are described below. The[ Beckn Core Spec](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml) should be referred to for authoritative descriptions on all attributes.


#### Acknowledging request (Transaction API)



1. The software must validate the incoming request for syntax and acknowledge with a success or failure (ACK/NACK) as described[ here](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-003-Beckn-Protocol-Communication-Draft-01.md). Essentially an ACK means that the software will send a response in due course. The format of the incoming messages must be verified using the Layer 2 Config file as described above. 


#### Sending Responses (Transaction API)



1. After the software acknowledges the request, it may trigger business logic to process the request. If the acknowledgement was an ACK, the software must respond back with a response. The format of the response must be valid against the Layer 2 Config file. The software should send a response (by calling these endpoints on the BAP subscriber url) from the following corresponding to the request (on_search for search, on_select for select etc). The endpoints to call and the required payload contents are:
    1. on_search - search result catalog
    2. on_select - quote for cart/order sent in select
    3. on_init - draft order with payment terms
    4. on_confirm - confirmation of order created
    5. on_status - status of requested order
    6. on_track - tracking details of requested order
    7. on_update - updated order
    8. on_cancel - canceled order
    9. on_rating - acknowledgement for rating
    10. on_support - requested support information


#### Handling response acknowledgements (Transaction API)



1. The recipient of the responses will send back an acknowledgement as described[ here](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-003-Beckn-Protocol-Communication-Draft-01.md). The software should suitably handle the failure cases (NACK).


#### Context (Transaction API)



1. The software must refer to[ Beckn Core Transaction API](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml) for the meaning of the context attributes and their usage. Some of attributes that have a major impact on functionality are listed here.
    1. **message_id**: The software in BPP mode must return the message_id unaltered in the corresponding response. For example the on_select of a select message should have the same message_id as the select message. If the software is sending a on_xxxxx message without a corresponding request (e.g. on_status on change of order status), then it must use a new message_id
    2. **transaction_id**: The software in BPP mode must send back the transaction_id unaltered in the corresponding response.
    3. **bap_id**: The software in BPP mode must send back the bap_id unaltered in the corresponding response.
    4. **bap_uri**: The software in BPP mode must send back the bap_uri unaltered in the corresponding response.
    5. **bpp_id**: The software in BPP mode must fill in this attribute with its own subscriber id.
    6. **bpp_uri**: The software in BPP mode must fill in this attribute with its own subscriber url.
    7. **ttl**: This defines the amount of time the message is considered active to correlate responses. Any responses for this message after this ttl must be dropped.


#### Responses without corresponding requests



1. In certain use cases such as order status update, the software in BPP mode may send an on_xxxx (e.g. on_status) message to the BAP. The software in BPP mode must use a new message_id.


#### XInput (Transaction API) 



1. When the BPP needs more information from the consumer, it may use the XInput facility to redirect the user to a BPP defined form. The implementation should be as described in[ The XInput Schema](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-007-The-XInput-Schema.md).


#### Error handling (Transaction API) 



1. In case of error during the processing of requests, the BPP must return back error responses. The format of the error messages is defined in the Beckn Core Transaction Specification. The codes to be used is specified in[ Error codes for BPP](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-005-Error-Codes-Draft-01.md)


#### Meta API - Listening and returning data 



1. The software in BPP mode must implement Meta API as described[ here](https://github.com/beckn/protocol-specifications/blob/master/api/meta/build/meta.yaml). These may be used by the BAP to assist in user interaction.


### Requirements for Registry mode


#### Common Requirements



1. All the requirements under the[ ](https://fide-official.atlassian.net/wiki/spaces/BBB/pages/edit-v2/572653580#Requirements-for-all-network-participants)“Requirements for all modes” section apply to the Registry mode.


#### Registration of Network Participants



1. Software in registry mode should allow Network Participant to register itself with a Subscriber ID and Subscriber URL
2. **Approval**: Software in registry mode may have approval criteria for registrants. Once the criteria are met, the registrants may be considered as subscribers as described[ here](https://developers.becknprotocol.io/docs/introduction/the-registry-infrastructure/).
3. **Ownership**: Software in registry mode must allow organizations / users that register Network Participants to own and have rights to modify their network roles and participant keys. It must not allow a user to change other user’s network participant details.


#### Subscription of Network Participants 



1. Software in registry mode must allow subscribers to create and alter their details including domain, role, location etc as described in[ Subscription](https://github.com/beckn/protocol-specifications/blob/master/schema/Subscription.yaml) and[ Subscriber](https://github.com/beckn/protocol-specifications/blob/master/schema/Subscriber.yaml) objects through the subscribe endpoint described below.


#### Network Participant - Keys



1. Software in registry mode must allow public key associated with network participants to be changed through the subscribe endpoint described below.


#### Subscribe (Registry API)



1. Software in registry mode must support the modification of subscriber information through the subscribe endpoint as described in[ Beckn Protocol Registry Infrastructure API](https://github.com/beckn/protocol-specifications/blob/master/api/registry/build/registry.yaml). It must support automatic verification of changed information through the call to on_subscribe as described in the API document.


#### Lookup (Registry API)



1. Software in registry mode must support the lookup of Subscriber information through the lookup endpoint as described in[ Beckn Protocol Registry Infrastructure API](https://github.com/beckn/protocol-specifications/blob/master/api/registry/build/registry.yaml). The lookup is a synchronous API call and must return its result in the response. It must support the following while it may support filtering by the rest of the parameters in[ subscriber](https://github.com/beckn/protocol-specifications/blob/master/schema/Subscriber.yaml) and[ subscription](https://github.com/beckn/protocol-specifications/blob/master/schema/Subscription.yaml) objects.
    1. lookup by subscriber_id and key_id
    2. lookup by domain and type


#### Logging - Public key changes 



1. All public key changes of the network participants must be logged and stored for extended duration. This is required for dispute resolutions at the trust layer.


#### Merging two networks



1. It should be possible to copy the network participant information present in the registry in bulk from one registry to another. 


### Requirements for Gateway mode


#### Common Requirements



1. All the requirements under the[ ](https://fide-official.atlassian.net/wiki/spaces/BBB/pages/edit-v2/572653580#Requirements-for-all-network-participants)“Requirements for all modes” section apply to the BAP mode.


#### Authentication - Key verification



1. In addition to the Authentication-Key-Pair general requirement for all modes, an on_subscribe endpoint must be made available for the registry to verify the public-key set there. The details are documented[ here](https://github.com/beckn/protocol-specifications/blob/master/api/registry/build/registry.yaml).


#### Authentication - Signing 



1. When the software in Gateway mode receives messages from the BAP to forward to the BPP, it must add its own signature and fill the X-Gateway-Authorization header. The process of hashing, signing and format of the header is documented[ here](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-006-Signing-Beckn-APIs-In-HTTP-Draft-01.md).


#### Listening to search requests (Transaction API)



1. The software in Gateway mode must expose the search endpoint at its Subscriber URL. The format of the message it receives is described[ here](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml).


#### Forwarding search (Transaction API)



1. When the Gateway receives a search request from the BAP, it must do the following:
    1. Use the lookup API of the Registry to fetch the BPPs that cater to the context of the received message.
    2. Forward the search request (after signing as described above) to the list of BPPs returned by the Registry.


#### Caching (Transaction API)



1. The software in Gateway mode should implement short term cache to reduce the load on the Registry lookup


## Non-Functional Requirements


### Performance

The system should be engineered to deliver optimal performance, ensuring quick and responsive interactions under various operating conditions. It should efficiently utilize resources to maintain a consistent user experience, even during periods of peak usage.


### Scalability

The system's architecture should support seamless scalability, enabling it to handle increased workloads and user demand with minimal effort. Both horizontal and vertical scaling strategies should be considered to ensure the system can grow alongside the needs of the business.

Every new network Participant in the Beckn network could potentially bring in a large amount of consumers or providers. The software should be designed to be scalable with respect to number of parallel requests being processed as well as latency.

The lookup API of the software in registry mode might potentially be called multiple times in a single message flow. It should be highly scalable and have low latency.

In the gateway mode, the software should be scalable with respect to the total time it takes to forward a search as the number of search requests and the number of BPPs increase. 


### Reliability and Availability

The software should be designed for high reliability, incorporating redundancy and fault-tolerant mechanisms to minimize downtime. It should maintain continuous availability, even in the event of hardware or software failures, ensuring users have consistent access to services.


### Usability

The software should be intuitive and user-friendly, allowing users to complete tasks efficiently with minimal learning curve. The design should adhere to best practices in usability and accessibility, providing a positive experience for all users, including those with varying abilities.


# Appendices


## APIs and Schemas

The API and schemas specification can be found here:

[https://github.com/beckn/protocol-specifications](https://github.com/beckn/protocol-specifications) 

The APIs can be browsed here: 

[https://beckn.github.io/beckn-onix/swagger/index.html](https://beckn.github.io/beckn-onix/swagger/index.html) 


## References



1. [Beckn Core Transaction Specification](https://github.com/beckn/protocol-specifications/blob/master/api/transaction/build/transaction.yaml)
2. [Beckn Core Registry Specification](https://github.com/beckn/protocol-specifications/blob/master/api/registry/build/registry.yaml)
3. [Beckn Protocol Meta API](https://github.com/beckn/protocol-specifications/blob/master/api/meta/build/meta.yaml)
4. [Signing Beckn APIs in HTTP](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-006-Signing-Beckn-APIs-In-HTTP-Draft-01.md)
5. [Beckn Protocol Communication](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-003-Beckn-Protocol-Communication-Draft-01.md)
6. [The XInput Schema](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-007-The-XInput-Schema.md)
7. [Error codes for BPP](https://github.com/beckn/protocol-specifications/blob/master/docs/BECKN-005-Error-Codes-Draft-01.md)
