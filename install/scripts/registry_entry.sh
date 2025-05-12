#!/bin/bash
source $SCRIPT_DIR/variables.sh

create_network_participant() {
    # Set your variables
        registry_url="$1"
        content_type="$2"
        subscriber_id="$3"
        pub_key_id="$4"
        subscriber_url="$5"
        encr_public_key="$6"
        signing_public_key="$7"
        valid_from="$8"
        valid_until="$9"
        type="${10}"
        api_key="${11}"
        np_domain="${12}"
    
    json_data=$(cat <<EOF
        {
            "subscriber_id": "$subscriber_id",
            "url": "$subscriber_url",
            "type": "$type",
            "domain": "${np_domain}",
            "location": {
                "city": {
                    "name": "Bangalore",
                    "code": "BLR"
                },
                "country": {
                    "name": "India", 
                    "code": "IN"
                }
            },
            "key_id": "$pub_key_id",
            "signing_public_key": "$signing_public_key",
            "encr_public_key": "$encr_public_key",
            "valid_from": "$valid_from",
            "valid_until": "$valid_until",
            "created": "$valid_from",
            "updated": "$valid_from",
            "nonce": "$pub_key_id"
}
        
EOF
)
    echo "json_data: $json_data"
    # response=$(curl --location "$registry_url/subscribers/subscribe" \
    # --header "Authorization: Bearer $api_key" \
    # --header "Content-Type: $content_type" \
    # --data "$json_data" 2>&1)
    if [ $? -eq 0 ]; then
        
        echo "${GREEN}Network Participant Entry is created. And subscribed to the registry $registry_url. ${NC}"
    else
        echo "${RED}Error: $response${NC}"
    fi
}
