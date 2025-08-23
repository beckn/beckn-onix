#!/bin/bash
get_container_ip() {
    container_name=$1
    container_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $container_name)
    echo $container_ip
}

#get_container_ip $1