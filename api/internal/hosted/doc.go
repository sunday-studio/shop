// Package hosted is the parent of the hosted (paid) mode building blocks. It holds no code;
// the scenario test in this directory wires the sub-packages together.
//
// Layout (no package here speaks HTTP; a thin HTTP layer is a later step):
//
//	hclock     injectable clock
//	control    control database (accounts, households, invites, devices, entitlements, ...)
//	grants     complimentary access and the Apple/grant combination rule
//	billing    App Store JWS verification, notification processor, plan calculator
//	appleauth  Sign in with Apple
//	limits     usage meter and limit checks
//	lifecycle  periodic jobs (lapse, deletion, reminders, erasure planning)
//	admin      admin CLI entry point
//	hostedtest test helpers (fake clock lives in hclock; PKI and signing helpers here)
//
// All of it can move to a private module later; the only dependency on the rest of the
// repository is none at all.
package hosted
