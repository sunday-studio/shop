package billing

import (
	"shop/internal/hosted/applepki"
	"shop/internal/hosted/hclock"
)

// NewPinnedAppleVerifier is the production Verifier: Apple Root CA - G3 pinned in the binary
// (package applepki) and Apple's marker OIDs required on the leaf and the intermediate. The
// chain is validated at the clock's now (fail closed for data signed under a certificate
// that has since expired). applepki.ChainVerifier.VerifyJWS is the stricter standalone
// variant (exactly three certificates, signedDate rule).
func NewPinnedAppleVerifier(clock hclock.Clock) *Verifier {
	cv := applepki.NewApple()
	return &Verifier{Roots: cv.Pool(), Clock: clock, LeafOID: applepki.LeafOID, IntermediateOID: applepki.IntermediateOID}
}
