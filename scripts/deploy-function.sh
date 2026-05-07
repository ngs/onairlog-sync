#!/bin/sh

set -eux

RUNTIME=go126
BASE_DIR=$(cd $(dirname $0)/.. && pwd)
FUNCTION=$1

cd $BASE_DIR

cat >.env.yml <<EOF
SLACK_WEBHOOK_URL: ${SLACK_WEBHOOK_URL}
PROJECT_ID: ${PROJECT_ID}
FIRESTORE_DATABASE: ${FIRESTORE_DATABASE}
EOF

TOPIC=$(echo $FUNCTION | sed 's/\([a-z0-9]\)\([A-Z]\)/\1-\2/g' | tr '[:upper:]' '[:lower:]')

gcloud functions deploy $FUNCTION \
    --trigger-topic $TOPIC \
    --runtime $RUNTIME \
    --service-account $SERVICE_ACCOUNT_EMAIL \
    --env-vars-file "${BASE_DIR}/.env.yml"
