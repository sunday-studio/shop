package billing

import "time"

// Notification types and subtypes used by the processor (App Store Server Notifications V2).
const (
	NtSubscribed             = "SUBSCRIBED"
	NtDidRenew               = "DID_RENEW"
	NtDidFailToRenew         = "DID_FAIL_TO_RENEW"
	NtGracePeriodExpired     = "GRACE_PERIOD_EXPIRED"
	NtExpired                = "EXPIRED"
	NtRefund                 = "REFUND"
	NtRefundReversed         = "REFUND_REVERSED"
	NtRevoke                 = "REVOKE"
	NtDidChangeRenewalStatus = "DID_CHANGE_RENEWAL_STATUS"
	NtDidChangeRenewalPref   = "DID_CHANGE_RENEWAL_PREF"
	NtOfferRedeemed          = "OFFER_REDEEMED"
	NtPriceIncrease          = "PRICE_INCREASE"
	NtRenewalExtended        = "RENEWAL_EXTENDED"
	NtRefundDeclined         = "REFUND_DECLINED"
	NtConsumptionRequest     = "CONSUMPTION_REQUEST"
	NtTest                   = "TEST"

	SubGracePeriod       = "GRACE_PERIOD"
	SubAutoRenewEnabled  = "AUTO_RENEW_ENABLED"
	SubAutoRenewDisabled = "AUTO_RENEW_DISABLED"
	DiscountFreeTrial    = "FREE_TRIAL"
	EnvProduction        = "Production"
	EnvSandbox           = "Sandbox"
	appleStatusActive    = 1
	appleStatusExpired   = 2
	appleStatusRetry     = 3
	appleStatusGrace     = 4
	appleStatusRevoked   = 5
)

// Ms is a time in milliseconds since the epoch, the way Apple sends it.
type Ms int64

// Time converts to time.Time (zero for 0).
func (m Ms) Time() time.Time {
	if m == 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(m)).UTC()
}

// MsOf converts a time to Ms (0 for the zero time).
func MsOf(t time.Time) Ms {
	if t.IsZero() {
		return 0
	}
	return Ms(t.UnixMilli())
}

// NotificationPayload is the decoded signedPayload (responseBodyV2DecodedPayload).
type NotificationPayload struct {
	NotificationType string           `json:"notificationType"`
	Subtype          string           `json:"subtype,omitempty"`
	NotificationUUID string           `json:"notificationUUID"`
	Version          string           `json:"version,omitempty"`
	SignedDate       Ms               `json:"signedDate"`
	Data             NotificationData `json:"data"`
}

// NotificationData is the "data" object.
type NotificationData struct {
	AppAppleID            int64  `json:"appAppleId,omitempty"`
	BundleID              string `json:"bundleId"`
	BundleVersion         string `json:"bundleVersion,omitempty"`
	Environment           string `json:"environment"`
	SignedTransactionInfo string `json:"signedTransactionInfo,omitempty"`
	SignedRenewalInfo     string `json:"signedRenewalInfo,omitempty"`
	Status                int    `json:"status,omitempty"`
}

// TransactionInfo is JWSTransactionDecodedPayload (the fields the server uses).
type TransactionInfo struct {
	TransactionID         string `json:"transactionId"`
	OriginalTransactionID string `json:"originalTransactionId"`
	BundleID              string `json:"bundleId"`
	ProductID             string `json:"productId"`
	PurchaseDate          Ms     `json:"purchaseDate,omitempty"`
	OriginalPurchaseDate  Ms     `json:"originalPurchaseDate,omitempty"`
	ExpiresDate           Ms     `json:"expiresDate,omitempty"`
	Type                  string `json:"type,omitempty"`
	AppAccountToken       string `json:"appAccountToken,omitempty"`
	InAppOwnershipType    string `json:"inAppOwnershipType,omitempty"`
	SignedDate            Ms     `json:"signedDate,omitempty"`
	OfferType             int    `json:"offerType,omitempty"`
	OfferIdentifier       string `json:"offerIdentifier,omitempty"`
	OfferDiscountType     string `json:"offerDiscountType,omitempty"`
	RevocationDate        Ms     `json:"revocationDate,omitempty"`
	RevocationReason      int    `json:"revocationReason,omitempty"`
	Environment           string `json:"environment,omitempty"`
}

// RenewalInfo is JWSRenewalInfoDecodedPayload (the fields the server uses).
type RenewalInfo struct {
	OriginalTransactionID  string `json:"originalTransactionId,omitempty"`
	AutoRenewProductID     string `json:"autoRenewProductId,omitempty"`
	AutoRenewStatus        int    `json:"autoRenewStatus"`
	ExpirationIntent       int    `json:"expirationIntent,omitempty"`
	GracePeriodExpiresDate Ms     `json:"gracePeriodExpiresDate,omitempty"`
	IsInBillingRetryPeriod bool   `json:"isInBillingRetryPeriod,omitempty"`
	RenewalDate            Ms     `json:"renewalDate,omitempty"`
	SignedDate             Ms     `json:"signedDate,omitempty"`
	Environment            string `json:"environment,omitempty"`
}

// IsFreeTrial reports whether the transaction was bought with a free-trial offer.
func (t TransactionInfo) IsFreeTrial() bool { return t.OfferDiscountType == DiscountFreeTrial }
