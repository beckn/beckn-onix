#!/bin/bash
update_env_file(){
    cp ../ENV/.env-generic-client-layer-sample ../ENV/.env-generic-client-layer
    envFile=../ENV/.env-generic-client-layer
    bap_subscriber_id=$1
    bap_subscriber_url=$2
    bap_client_url=$3

    if [[ $(uname) == "Darwin" ]]; then
        sed -i '' "s|BAP_SUBSCRIBER_ID|$bap_subscriber_id|" $envFile
        sed -i '' "s|BAP_SUBSCRIBER_URL|$bap_subscriber_url|" $envFile
        sed -i '' "s|BAP_CLIENT_URL|$bap_client_url|" $envFile
    else
        sed -i "s|BAP_SUBSCRIBER_ID|$bap_subscriber_id|" $envFile
        sed -i "s|BAP_SUBSCRIBER_URL|$bap_subscriber_url|" $envFile
        sed -i "s|BAP_CLIENT_URL|$bap_client_url|" $envFile
    fi

}

update_env_file $1 $2 $3