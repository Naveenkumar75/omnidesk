package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

// Token issuer and audience. The AI service checks the same values, so a
// token minted for some other system with the same secret is rejected.
const (
	TokenIssuer   = "omnidesk"
	TokenAudience = "omnidesk-api"
)

// Roles. Admins manage users and the knowledge base; agents work tickets.
const (
	RoleAdmin = "admin"
	RoleAgent = "agent"
)

func ValidRole(r string) bool { return r == RoleAdmin || r == RoleAgent }

// bcrypt only uses the first 72 bytes and x/crypto rejects longer input, so
// longer passwords are refused up front instead of failing later with a 500.
const (
	MinPasswordLen = 8
	MaxPasswordLen = 72
)

var ErrPasswordLength = errors.New("password must be 8 to 72 bytes")

func HashPassword(plain string) (string, error) {
	if len(plain) < MinPasswordLen || len(plain) > MaxPasswordLen {
		return "", ErrPasswordLength
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	return string(h), err
}

func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// dummyHash has the same cost as real hashes. Comparing against it when the
// email is unknown makes a failed login take the same time either way, so
// response timing does not reveal which emails have accounts.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("omnidesk-timing-equaliser"), bcryptCost)

// BurnPasswordCheck spends the same time as CheckPassword on a real account.
func BurnPasswordCheck(plain string) {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(plain))
}

// Claims carries the identity used to scope every query to a tenant.
type Claims struct {
	UserID   int    `json:"user_id"`
	TenantID int    `json:"tenant_id"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

func (i *Issuer) Issue(userID, tenantID int, role string) (string, error) {
	now := time.Now()
	c := Claims{
		UserID:   userID,
		TenantID: tenantID,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    TokenIssuer,
			Audience:  jwt.ClaimStrings{TokenAudience},
			Subject:   strconv.Itoa(userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(i.secret)
}

func (i *Issuer) Parse(token string) (*Claims, error) {
	c := &Claims{}
	t, err := jwt.ParseWithClaims(token, c, func(t *jwt.Token) (any, error) {
		return i.secret, nil
	},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(TokenIssuer),
		jwt.WithAudience(TokenAudience),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !t.Valid || c.TenantID < 1 || c.UserID < 1 || !ValidRole(c.Role) {
		return nil, errors.New("invalid token")
	}
	return c, nil
}

type ctxKey struct{}

// Middleware rejects requests without a valid bearer token and stores the
// claims in the request context.
func (i *Issuer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		claims, err := i.Parse(raw)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, claims)))
	})
}

// RequireRole allows the request only for the given roles. It must run after
// Middleware.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := FromContext(r.Context())
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}
			for _, role := range roles {
				if c.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			writeJSONError(w, http.StatusForbidden, "this action requires role: "+strings.Join(roles, " or "))
		})
	}
}

func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(*Claims)
	return c, ok
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":` + strconv.Quote(msg) + `}`))
}
