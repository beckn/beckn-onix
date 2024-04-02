## Introduction

The [demo walkthrough](../docs/demo_walkthrough.md) and the accompanying video shows a story where we have the following steps

1. We install a core Beckn network using Beckn-ONIX
2. We try to conduct a retail transaction, but it fails due to layer 2 config not being present
3. We install the layer 2 config for retail from and successfully complete a retail transaction
4. We try to perform a query to find charging station nearby (energy transaction), but it fails due to the layer 2 for energy being absent
5. We install the layer 2 config for energy
6. Now the energy transaction completes.

In this document we list a few other demo stories where we can show a similar flow but with different domains. The steps are identical, but the story changes to involve different domains.

## Healthcare + Mobility

Alice wants to book a wellness center appointment and then book a cab to reach the clinic.

1. We install a core Beckn network using Beckn-ONIX
2. Alice tries to conduct a healthcare booking, but it fails due to layer 2 config not being present
3. We install the layer 2 config for healthcare `https://raw.githubusercontent.com/beckn/beckn-onix/main/layer2/samples/dhp_1.1.0.yaml` and successfully complete a healthcare appointment booking
4. She tries to book a cab to reach the clinic, but it fails due to the layer 2 for mobility not being present
5. We install the layer 2 config for mobility `https://raw.githubusercontent.com/beckn/beckn-onix/main/layer2/samples/mobility_1.1.0.yaml`
6. Now Alice can book a cab and reach the clinic.

## Mobility + Retail

Bob wants to book a cab back home. He wants to buy groceries so he can pick it up on the way

1. We install a core Beckn network using Beckn-ONIX
2. Bob tries to book a cab, but it fails due to layer 2 config not being present
3. We install the layer 2 config for mobility `https://raw.githubusercontent.com/beckn/beckn-onix/main/layer2/samples/mobility_1.1.0.yaml` and successfully complete the booking
4. He now wants to buy groceries from a shop on the way, but it fails due to the layer 2 for retail not being present
5. We install the layer 2 config for retail `https://raw.githubusercontent.com/beckn/beckn-onix/main/layer2/samples/retail_1.1.0.yaml`
6. Now Bob can complete the retail order and pick it up on the way home

## Healthcare + Energy

Cindy wants to schedule a wellness clinic visit, find charging stations near the clinic to get her bike charged while she is attending her appointment

1. We install a core Beckn network using Beckn-ONIX
2. Cindy tries to book a wellness clinic appointment. It fails due to healthcare layer 2 being missing
3. We install the layer 2 config for healthcare `https://raw.githubusercontent.com/beckn/beckn-onix/main/layer2/samples/dhp_1.1.0.yaml` and Cindy can successfully book the appointment
4. Now she wants to find charging stations near the clinic, but it fails as energy layer 2 config is absent.
5. We install the layer 2 config for energy `https://raw.githubusercontent.com/beckn/beckn-onix/main/layer2/samples/uei_charging_1.1.0.yaml`
6. Now Cindy can find charging stations near the clinic where she can drop the bike and have it charged while finishing her appointment
