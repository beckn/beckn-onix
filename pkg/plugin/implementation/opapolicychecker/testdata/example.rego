package policy

import rego.v1

# Example policy: validation rules for beckn messages.
# This demonstrates the structured result format used with policy_query_path.
#
# Available inputs:
#   - input: the full JSON message body (beckn request)
#   - data.config: runtime config from the adapter YAML (e.g., minDeliveryLeadHours)

# Default result: valid with no violations.
default result := {
  "valid": true,
  "violations": []
}

# Compute the result from collected violations.
result := {
  "valid": count(violations) == 0,
  "violations": violations
}

# Require provider details on confirm
violations contains "confirm: missing provider in order" if {
    input.context.action == "confirm"
    not input.message.order.provider
}

# Require at least one fulfillment on confirm
violations contains "confirm: order has no fulfillments" if {
    input.context.action == "confirm"
    not input.message.order.fulfillments
}

# Require billing details on confirm
violations contains "confirm: missing billing info" if {
    input.context.action == "confirm"
    not input.message.order.billing
}

# Require payment details on confirm
violations contains "confirm: missing payment info" if {
    input.context.action == "confirm"
    not input.message.order.payment
}

# Require search intent
violations contains "search: missing intent" if {
    input.context.action == "search"
    not input.message.intent
}
