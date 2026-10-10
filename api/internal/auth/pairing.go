package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"shop/internal/store"
	"shop/internal/store/db"
)

// Pairing: the server has one password, set at install time. A phone
// exchanges URL + password + device name for its own bearer token once; the
// password is never stored on the device and each device can be revoked.

const (
	passwordKey       = "pairing_password_bcrypt"
	MinPasswordLength = 10
	bcryptCost        = 12
)

var (
	ErrNoPassword    = errors.New("no pairing password set; run `shop password generate` on the server")
	ErrWrongPassword = errors.New("wrong password")
	ErrWeakPassword  = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
)

// SetPassword replaces the pairing password.
func SetPassword(ctx context.Context, q *db.Queries, password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return ErrWeakPassword
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	return q.PutSetting(ctx, db.PutSettingParams{Key: passwordKey, Value: string(h), UpdatedAt: store.Now()})
}

// HasPassword reports whether pairing is possible.
func HasPassword(ctx context.Context, q *db.Queries) (bool, error) {
	_, err := q.GetSetting(ctx, passwordKey)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// CheckPassword verifies the pairing password (constant time via bcrypt).
func CheckPassword(ctx context.Context, q *db.Queries, password string) error {
	h, err := q.GetSetting(ctx, passwordKey)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoPassword
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(h), []byte(password)) != nil {
		return ErrWrongPassword
	}
	return nil
}

// Pair checks the password and issues a device token. Device names are made
// unique ("iPhone", "iPhone (2)", …).
func Pair(ctx context.Context, q *db.Queries, password, device string) (string, db.ApiToken, error) {
	if err := CheckPassword(ctx, q, password); err != nil {
		return "", db.ApiToken{}, err
	}
	device = strings.TrimSpace(device)
	if device == "" {
		device = "device"
	}
	if len(device) > 60 {
		device = device[:60]
	}
	name := device
	for i := 2; ; i++ {
		n, err := q.TokenNameExists(ctx, name)
		if err != nil {
			return "", db.ApiToken{}, err
		}
		if n == 0 {
			break
		}
		name = fmt.Sprintf("%s (%d)", device, i)
	}
	tok := generate()
	row, err := q.CreateToken(ctx, db.CreateTokenParams{Name: name, TokenHash: Hash(tok), Kind: "device", CreatedAt: store.Now()})
	return tok, row, err
}

// GeneratePassphrase returns a random, easy-to-type passphrase like
// "maple-orbit-cedar-tide-42": 4 words from ~240 plus 2 digits ≈ 38 bits,
// plenty with pairing attempts rate-limited.
func GeneratePassphrase() string {
	pick := func(n int) int {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
		if err != nil {
			panic(err)
		}
		return int(v.Int64())
	}
	parts := make([]string, 0, 5)
	for range 4 {
		parts = append(parts, words[pick(len(words))])
	}
	parts = append(parts, fmt.Sprintf("%02d", pick(100)))
	return strings.Join(parts, "-")
}

// 256 short, distinct, easy-to-spell English words.
var words = strings.Fields(`
acorn amber anchor apple arch arrow aspen atlas autumn badge bamboo banjo barley basil beacon bean
berry birch bison blade bloom bolt bonsai brave breeze brick brook bubble cactus camel canal candle
canyon cargo carrot cedar cello chalk cherry chess cider cinder cliff clover cobalt comet copper coral
cosmos cotton crane crater creek cricket crown cypress dahlia daisy delta denim desert diesel dingo dock
dolphin dove dragon drum dune eagle echo ember emerald falcon fern fiddle field fig flame flint flute
forest fossil fox frost galaxy garnet gecko geyser ginger glacier globe granite grape gravel harbor hazel
heron hickory honey hornet husky iceberg indigo iris island ivory jade jasmine jelly jet jungle kayak
kettle kiwi koala lagoon lantern larch lava lemon lily linen lizard llama lotus lunar lynx magnet mango
maple marble meadow melon mesa meteor mint mocha monsoon moose mosaic moss motor nectar needle nickel
nova nutmeg oak oasis ocean olive onyx opal orbit orchid otter oyster paddle palm panda paper parrot
pebble pepper piano pine pixel planet plum polar pony poppy prairie prism puffin quartz quill rabbit
radar raven reef ribbon ridge river robin rocket rose ruby saffron sage salmon sand sapphire satin
scarlet seal shadow shell sierra silver sky slate snow sonar spark spice spruce squid star stone storm
summit sun swan tango thistle thunder tide tiger timber topaz torch tulip tundra turtle umber valley
velvet violet volcano walnut walrus wave willow wind wolf yarrow zebra zenith zinc
`)
