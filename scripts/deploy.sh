#!/bin/sh

set -eux

FUNCTIONS="
  HourlySync
  HubspotDealsInitial
  HubspotDealsLatest
  HubspotPipelinesSync
  HubspotContactsInitial
  HubspotContactsLatest
  HubspotCompaniesInitial
  HubspotCompaniesLatest
  HubspotOwnersSync
"

/bin/sh "${BASE_DIR}/scripts/sync-config.sh"

for FUNCTION in $FUNCTIONS; do
  /bin/sh "${BASE_DIR}/scripts/deploy-function.sh" $FUNCTION
done
