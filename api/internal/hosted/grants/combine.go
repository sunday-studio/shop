// Package grants implements complimentary access: admin grants with a mandatory expiry,
// a reason and an audit trail, plus the rule that combines an Apple entitlement with a grant.
//
// No code field and no redeem route exist in the app (App Review guideline 3.1.1); a grant is a
// server-side decision about an account.
package grants

import "time"

// Level is how much access one source gives right now.
type Level int

const (
	LevelNone     Level = iota // gives nothing
	LevelReadOnly              // read and export only (Apple billing retry)
	LevelFull                  // everything the plan allows
)

// Access is what one source (Apple or a grant) gives at a point in time.
type Access struct {
	Source   string // "apple" or "grant"
	Level    Level
	Until    time.Time // when this access ends if nothing renews (zero = unknown)
	Renewing bool      // Apple subscription that will renew: treated as open-ended
}

func (a Access) endsAfter(b Access) bool {
	if a.Renewing != b.Renewing {
		return a.Renewing
	}
	if a.Renewing {
		return false // both open-ended: not strictly later
	}
	return a.Until.After(b.Until)
}

// Combine applies the combination rule. The better source wins:
//
//  1. a higher Level beats a lower one (full > read-only > none);
//  2. between two full (or two read-only) sources the one that lasts longer wins, a renewing
//     Apple subscription counting as open-ended;
//  3. a tie goes to Apple (the contract shows entitlement_source "apple" on ties).
//
// The household therefore never loses access because a second source exists: a grant added
// on top of a paying subscription is invisible until the subscription ends, and a grant that
// ends first leaves the subscription in charge.
func Combine(apple, grant Access) Access {
	apple.Source, grant.Source = "apple", "grant"
	switch {
	case apple.Level > grant.Level:
		return apple
	case grant.Level > apple.Level:
		return grant
	case apple.Level == LevelNone:
		return Access{Level: LevelNone}
	case grant.endsAfter(apple):
		return grant
	default:
		return apple
	}
}
