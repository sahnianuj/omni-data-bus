package export

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strings"
	"unicode"
	"unicode/utf8"

	"omnidatabus/internal/auth"

	"golang.org/x/crypto/argon2"
)

// DefaultKDF is the sealed mode's key derivation (architecture D4): argon2id, 3 passes over
// 64 MiB, one lane.
var DefaultKDF = KDF{Alg: "argon2id", T: 3, M: 64 * 1024, P: 1}

const keyLen = 32 // AES-256

// Secrets are a connection's credentials in clear text; they live only in memory.
type Secrets map[string]string

// Seal encrypts secrets under a key derived from passphrase, bound to the bundle content hash
// aad (OC-052).
func Seal(secrets Secrets, passphrase, aad string, kdf KDF) (*Credentials, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	kdf.Salt = base64.StdEncoding.EncodeToString(salt)
	key := deriveKey(passphrase, salt, kdf)
	defer zero(key)
	plain, err := json.Marshal(secrets)
	if err != nil {
		return nil, err
	}
	defer zero(plain)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, plain, []byte(aad))
	return &Credentials{
		Mode: ModeSealed, KDF: &kdf,
		Nonce: base64.StdEncoding.EncodeToString(nonce), Ciphertext: base64.StdEncoding.EncodeToString(ct),
	}, nil
}

// ErrPassphrase is any failure to open a sealed block: a wrong passphrase, a corrupted
// ciphertext and a block moved from another bundle all look the same.
var ErrPassphrase = &Error{Code: CodePassphraseInvalid, Message: "The passphrase doesn't open this bundle's credentials."}

// Unseal decrypts a sealed block. Every failure after the key derivation returns ErrPassphrase,
// so timing and errors don't tell a wrong passphrase from a damaged block.
func Unseal(c *Credentials, passphrase, aad string) (Secrets, error) {
	if c == nil || c.Mode != ModeSealed || c.KDF == nil {
		return nil, &Error{Code: CodeInvalid, Message: "The credentials aren't sealed."}
	}
	if c.KDF.Alg != "argon2id" || c.KDF.T == 0 || c.KDF.M == 0 || c.KDF.P == 0 || c.KDF.M > 4<<20 || c.KDF.T > 64 {
		return nil, &Error{Code: CodeInvalid, Message: "The sealed credentials use unsupported key derivation settings."}
	}
	salt, err1 := base64.StdEncoding.DecodeString(c.KDF.Salt)
	nonce, err2 := base64.StdEncoding.DecodeString(c.Nonce)
	ct, err3 := base64.StdEncoding.DecodeString(c.Ciphertext)
	key := deriveKey(passphrase, salt, *c.KDF) // always derived, even for a malformed block
	defer zero(key)
	if err := errors.Join(err1, err2, err3); err != nil {
		return nil, ErrPassphrase
	}
	gcm, err := newGCM(key)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, ErrPassphrase
	}
	plain, err := gcm.Open(nil, nonce, ct, []byte(aad))
	if err != nil {
		return nil, ErrPassphrase
	}
	defer zero(plain)
	var s Secrets
	if err := json.Unmarshal(plain, &s); err != nil {
		return nil, ErrPassphrase
	}
	return s, nil
}

func deriveKey(passphrase string, salt []byte, k KDF) []byte {
	return argon2.IDKey([]byte(passphrase), salt, k.T, k.M, k.P, keyLen)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// MinPassphraseLen is the shortest passphrase accepted.
const MinPassphraseLen = 12

// minPassphraseBits is the strength a passphrase needs, estimated as below (four diceware words
// are 51.7 bits; 12 random letters and digits about 62).
const minPassphraseBits = 50

// PassphraseProblem returns why a passphrase is too weak, or "". The web dialog applies the
// same rule (web/src/features/export/passphrase.ts), so both sides agree.
func PassphraseProblem(p string) string {
	if utf8.RuneCountInString(p) < MinPassphraseLen {
		return "must be at least 12 characters"
	}
	if msg := auth.ValidatePassword(p, ""); msg != "" {
		return msg
	}
	if PassphraseBits(p) < minPassphraseBits {
		return "is too predictable: use more words or characters (Generate makes a strong one)"
	}
	return ""
}

// PassphraseBits estimates a passphrase's entropy. Words from the diceware list count 12.9 bits
// each; any other run counts log2(character pool) per distinct character, so repeats and
// keyboard runs ("aaaa…", "1234…") count for little.
func PassphraseBits(p string) float64 {
	fields := strings.FieldsFunc(strings.ToLower(p), func(r rune) bool { return r == ' ' || r == '-' || r == '.' || r == '_' })
	words := 0
	var rest []string
	for _, f := range fields {
		if _, ok := wordSet[f]; ok {
			words++
		} else {
			rest = append(rest, f)
		}
	}
	bits := float64(words) * math.Log2(float64(len(wordList)))
	for _, r := range rest {
		bits += runBits(r)
	}
	return bits
}

func runBits(s string) float64 {
	pool := 0
	var lower, upper, digit, other bool
	distinct := map[rune]bool{}
	var prev rune
	steps := 0
	for i, r := range s {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		default:
			other = true
		}
		// A character one more or less than the previous one ("abc", "321") adds nothing.
		if i == 0 || (r != prev+1 && r != prev-1) {
			distinct[r] = true
			steps++
		}
		prev = r
	}
	for _, on := range []struct {
		set  bool
		size int
	}{{lower, 26}, {upper, 26}, {digit, 10}, {other, 33}} {
		if on.set {
			pool += on.size
		}
	}
	if pool == 0 {
		return 0
	}
	n := min(len(distinct), steps)
	return float64(n) * math.Log2(float64(pool))
}

//go:embed eff_large_wordlist.txt
var wordListRaw string // EFF large word list (CC BY 3.0, https://www.eff.org/dice)

var wordList, wordSet = func() ([]string, map[string]struct{}) {
	var list []string
	set := map[string]struct{}{}
	sc := bufio.NewScanner(strings.NewReader(wordListRaw))
	for sc.Scan() {
		if w := strings.TrimSpace(sc.Text()); w != "" {
			list = append(list, w)
			set[w] = struct{}{}
		}
	}
	return list, set
}()

// GeneratePassphrase returns six random diceware words joined by dashes (77 bits).
func GeneratePassphrase() (string, error) {
	words := make([]string, 6)
	n := big.NewInt(int64(len(wordList)))
	for i := range words {
		k, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		words[i] = wordList[k.Int64()]
	}
	return strings.Join(words, "-"), nil
}
