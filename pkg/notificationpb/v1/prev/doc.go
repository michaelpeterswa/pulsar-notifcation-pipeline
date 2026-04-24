// Package notificationpbv1prev is a frozen snapshot of the previously released
// generation of notificationpbv1, used exclusively by the compat test in the
// parent package to verify forward and backward wire compatibility (FR-013,
// SC-007). Do not import this package from production code.
//
// Rotation procedure: after a deliberate schema change, replace the contents
// of this directory with the prior tag's generated notification.pb.go and
// rename its `package notificationpbv1` declaration to `notificationpbv1prev`
// (one sed line; see the Makefile task T013 in specs/001-notification-pipeline/
// tasks.md).
package notificationpbv1prev
