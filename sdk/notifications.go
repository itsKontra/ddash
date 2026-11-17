package sdk

import (
	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"
)

// Notifications returns the client of the core's notification service.
// Every Component may send a Notification, whatever it requires: the host
// serves the service itself, audits each Send and delivers it to every
// installed channel whose filter takes its category (ADR-0010). In tests
// it returns the fake given to sdktest.WithClient.
func Notifications(env *Env) notificationpb.NotificationServiceClient {
	return Require(env, notificationpb.NewNotificationServiceClient)
}
