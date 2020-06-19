# onairlog-sync

## Run functions locally

```sh
gcloud auth application-default login
# Then OAuth on the browser

go run github.com/ngs/onairlog-sync/local

# On the other terminal tab
curl http://localhost:8080/sync -d '{"data":""}'
JSON=$(cat fixtures/songs.json)
curl http://localhost:8080/notify -d "{\"data\":\"$(echo $JSON | base64)\"}"
```
