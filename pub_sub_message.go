package onairlogsync

// PubSubMessage is the Pub/Sub payload nested inside a Cloud Functions Gen 2
// CloudEvent of type google.cloud.pubsub.topic.v1.messagePublished. The
// CloudEvent's Data() is JSON of the form {"message": {"data": "<base64>"},
// "subscription": "..."}.
type PubSubMessage struct {
	Message struct {
		Data []byte `json:"data"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}
