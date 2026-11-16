package utils

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const passwordIterations = 120000

func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := pbkdf2SHA256([]byte(password), salt, passwordIterations, 32)
	return fmt.Sprintf(
		"pbkdf2_sha256$%d$%s$%s",
		passwordIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(encoded string, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	actual := pbkdf2SHA256([]byte(password), salt, iterations, len(expected))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func ValidatePasswordHash(encoded string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return errors.New("unsupported password hash")
	}
	return nil
}

func pbkdf2SHA256(password, salt []byte, iterations int, keyLen int) []byte {
	hashLen := sha256.Size
	numBlocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, numBlocks*hashLen)
	// One keyed HMAC is reused for every round; Reset keeps the precomputed
	// inner/outer pads, so the result is identical but avoids per-round setup.
	mac := hmac.New(sha256.New, password)
	u := make([]byte, 0, hashLen)
	t := make([]byte, hashLen)
	var counter [4]byte
	for block := 1; block <= numBlocks; block++ {
		mac.Reset()
		mac.Write(salt)
		binary.BigEndian.PutUint32(counter[:], uint32(block))
		mac.Write(counter[:])
		u = mac.Sum(u[:0])
		copy(t, u)
		for i := 1; i < iterations; i++ {
			mac.Reset()
			mac.Write(u)
			u = mac.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

// dummyPasswordHash lets login spend the same PBKDF2 work for unknown
// accounts, so response time does not reveal which emails are registered.
var dummyPasswordHash = func() string {
	hash, err := HashPassword("origin-timing-equalizer")
	if err != nil {
		return "pbkdf2_sha256$120000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	return hash
}()

// VerifyPasswordOrDummy behaves like VerifyPassword but always performs a full
// hash computation, even when the stored hash is missing.
func VerifyPasswordOrDummy(encoded string, found bool, password string) bool {
	if !found {
		VerifyPassword(dummyPasswordHash, password)
		return false
	}
	return VerifyPassword(encoded, password)
}
