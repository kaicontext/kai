// Package auth provides authentication and authorization for the control plane.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidToken     = errors.New("invalid token")
	ErrTokenExpired     = errors.New("token expired")
	ErrInvalidSignature = errors.New("invalid signature")
)

// Claims represents the JWT claims.
type Claims struct {
	jwt.RegisteredClaims
	UserID   string   `json:"uid"`
	Email    string   `json:"email"`
	Orgs     []string `json:"orgs,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
	TokenID  string   `json:"tid,omitempty"` // For PAT tracking
	Audience string   `json:"aud,omitempty"`
}

// TokenService provides JWT and token operations.
type TokenService struct {
	signingKey []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewTokenService creates a new TokenService.
func NewTokenService(signingKey []byte, issuer string, accessTTL, refreshTTL time.Duration) *TokenService {
	return &TokenService{
		signingKey: signingKey,
		issuer:     issuer,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}
}

// GenerateAccessToken generates a short-lived access JWT.
func (s *TokenService) GenerateAccessToken(userID string, email string, orgs, scopes []string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.accessTTL)),
		},
		UserID: userID,
		Email:  email,
		Orgs:   orgs,
		Scopes: scopes,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.signingKey)
}

// GenerateDownstreamToken generates a short-lived token for kailabd.
func (s *TokenService) GenerateDownstreamToken(userID string, email, org string, scopes []string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   userID,
			Audience:  jwt.ClaimStrings{"kailabd"},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(60 * time.Second)), // Short-lived
		},
		UserID:   userID,
		Email:    email,
		Orgs:     []string{org},
		Scopes:   scopes,
		Audience: "kailabd",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.signingKey)
}

// ValidateAccessToken validates and parses an access token.
func (s *TokenService) ValidateAccessToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidSignature
		}
		return s.signingKey, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// GenerateRefreshToken generates a cryptographically random refresh token.
func GenerateRefreshToken() (token string, hash string, err error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", err
	}
	token = base64.URLEncoding.EncodeToString(bytes)
	hash = HashToken(token)
	return token, hash, nil
}

// GenerateMagicLink generates a magic link token.
func GenerateMagicLink() (token string, hash string, err error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", err
	}
	token = base64.URLEncoding.EncodeToString(bytes)
	hash = HashToken(token)
	return token, hash, nil
}

// GeneratePAT generates a personal access token.
func GeneratePAT() (token string, hash string, err error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", err
	}
	// Format: klc_<base64> (klc = kailab control)
	token = "klc_" + base64.URLEncoding.EncodeToString(bytes)
	hash = HashToken(token)
	return token, hash, nil
}

// HashToken hashes a token using SHA-256.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// HashPassword hashes a password using bcrypt.
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPassword checks a password against a hash.
func CheckPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ExtractBearerToken extracts the token from an Authorization header.
func ExtractBearerToken(authHeader string) string {
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}

// IsPAT checks if a token is a PAT (starts with klc_).
func IsPAT(token string) bool {
	return strings.HasPrefix(token, "klc_")
}
