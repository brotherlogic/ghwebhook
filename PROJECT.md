# Github Webhook

GH Webhook is a github webhook proxy for kubernetes - it places a single
webhook handler in a cluster and then proxies updates from the webhhok
to the relevant handlers. In doing so it turns the webhook into a proto
object, so that updates can be managed via grpc.

## Webhook types

We currently handle the following types of incoming webhooks:

* PR Creation / Updates
* Issue Creation / Updates

The GHWebhook receives incoming webhooks, converts them into a proto
format and then calls a handler function on a registered service.

## Service registration

The registration process maps a repo to a service, the service must be running
and implement the GHWebhook service.

## Webhook handling

On receiving a webhook, the service is converted into a proto, the repo
extracted from that proto, and then either sent to the services which
have registered for that repo, or dropped if no such service exists.

We track delivery failures internally, three failures in a row will cause
the registration for that service to be dropped.