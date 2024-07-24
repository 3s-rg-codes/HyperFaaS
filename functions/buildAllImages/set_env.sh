#!/bin/sh

CONTAINER_ID=$(cat /proc/self/mountinfo | grep "/docker/containers/" | head -1 | awk '{print $4}' | sed 's/\/var\/lib\/docker\/containers\///g' | sed 's/\/resolv.conf//g' | xargs basename)
echo "CONTAINER_ID=$CONTAINER_ID" > /root/.env