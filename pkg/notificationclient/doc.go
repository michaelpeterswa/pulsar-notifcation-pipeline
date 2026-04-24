// Package notificationclient is the importable Go HTTP client for the
// pulsar-notifcation-pipeline writer API.
//
// # Quick start
//
//	client, err := notificationclient.New("https://writer.example",
//	    notificationclient.WithBearerToken("abc-123"))
//	if err != nil { log.Fatal(err) }
//
//	resp, err := client.Submit(ctx, notificationclient.NewPushoverRequest(
//	    "uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "Deploy finished", "Build #42 succeeded"))
//	if err != nil {
//	    var perr *notificationclient.ProblemError
//	    if errors.As(err, &perr) && errors.Is(perr, notificationclient.ErrValidationFailed) {
//	        for _, fe := range perr.FieldErrors() {
//	            log.Printf("  %s: %s", fe.Field, fe.Code)
//	        }
//	    }
//	    log.Fatal(err)
//	}
//	log.Printf("accepted: id=%s request_id=%s", resp.NotificationID(), resp.RequestID())
//
// Protobuf content type is also supported — pass
// notificationclient.WithContentType(notificationclient.ContentTypeProtobuf)
// to New to submit the canonical message as protobuf bytes rather than JSON.
// Responses are always JSON regardless (FR-023); client code does not need to
// care about the wire format to call the API.
package notificationclient
