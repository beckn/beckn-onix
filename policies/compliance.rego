package policy

import rego.v1

# Example policy: validate confirm action messages.
# This is a sample policy file. Replace with your actual business rules.
#
# The policy evaluates incoming beckn messages and produces a set of
# violation strings. If any violations exist, the adapter will NACK
# the request.
#
# Available inputs:
#   - input: the full JSON message body
#   - data.config: runtime config from the adapter config (e.g., minDeliveryLeadHours)

# violations is the set of policy violation messages.
# An empty set means the message is compliant.
violations := set()
