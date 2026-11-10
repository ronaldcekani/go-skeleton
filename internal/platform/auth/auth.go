// Package auth verifies Keycloak access tokens. Verification is local: the
// realm's signing keys (JWKS) are fetched once and refreshed in the
// background, so no request to Keycloak is made per API call.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/time/rate"

	"github.com/example/go-skeleton/internal/domain"
)

type Options struct {
	Issuer   string
	JWKSURL  string // empty: Issuer + "/protocol/openid-connect/certs"
	Audience string // empty: the "aud" claim is not checked
	Leeway   time.Duration
}

// Authenticator turns a raw bearer token into a domain.Principal.
type Authenticator struct {
	keys   keyfunc.Keyfunc
	parser *jwt.Parser
	stop   context.CancelFunc
}

const (
	// keyRefreshInterval is how often the signing keys are re-fetched.
	keyRefreshInterval = 15 * time.Minute
	// unknownKeyRefreshInterval limits the extra fetches triggered by a
	// token signed with a key that is not cached yet (a key rotation, or
	// Keycloak having been unreachable at startup). The limit keeps forged
	// tokens with random key IDs from hammering Keycloak.
	unknownKeyRefreshInterval = 30 * time.Second
)

// New starts the background key refresh. If Keycloak is unreachable at
// startup the app still starts; tokens are rejected until the keys have been
// fetched.
func New(opts Options, log *slog.Logger) (*Authenticator, error) {
	jwksURL := opts.JWKSURL
	if jwksURL == "" {
		jwksURL = strings.TrimRight(opts.Issuer, "/") + "/protocol/openid-connect/certs"
	}

	// The refresh goroutine lives until Close; it must not be tied to a
	// startup context that may be cancelled earlier.
	ctx, stop := context.WithCancel(context.Background())
	storage, err := jwkset.NewStorageFromHTTP(jwksURL, jwkset.HTTPClientStorageOptions{
		Ctx:                       ctx,
		HTTPTimeout:               10 * time.Second,
		NoErrorReturnFirstHTTPReq: true,
		RefreshInterval:           keyRefreshInterval,
		RefreshErrorHandler: func(ctx context.Context, err error) {
			log.ErrorContext(ctx, "fetching keycloak signing keys failed",
				slog.String("url", jwksURL), slog.Any("error", err))
		},
	})
	if err != nil {
		stop()
		return nil, fmt.Errorf("create jwks storage for %q: %w", jwksURL, err)
	}
	client, err := jwkset.NewHTTPClient(jwkset.HTTPClientOptions{
		HTTPURLs:          map[string]jwkset.Storage{jwksURL: storage},
		RefreshUnknownKID: rate.NewLimiter(rate.Every(unknownKeyRefreshInterval), 1),
		// A request whose token has an unknown key ID fails right away
		// instead of queueing for the next allowed refresh.
		RateLimitWaitMax: time.Millisecond,
	})
	if err != nil {
		stop()
		return nil, fmt.Errorf("create jwks client for %q: %w", jwksURL, err)
	}
	keys, err := keyfunc.New(keyfunc.Options{Ctx: ctx, Storage: client})
	if err != nil {
		stop()
		return nil, fmt.Errorf("create jwt keyfunc: %w", err)
	}

	parserOpts := []jwt.ParserOption{
		// Asymmetric algorithms only: this rules out "none" and HMAC tokens
		// signed with a public key.
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}),
		jwt.WithIssuer(opts.Issuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(opts.Leeway),
	}
	if opts.Audience != "" {
		parserOpts = append(parserOpts, jwt.WithAudience(opts.Audience))
	}
	return &Authenticator{keys: keys, parser: jwt.NewParser(parserOpts...), stop: stop}, nil
}

// Close stops the background key refresh.
func (a *Authenticator) Close() { a.stop() }

// claims are the parts of a Keycloak access token the application uses.
type claims struct {
	jwt.RegisteredClaims
	Type              string `json:"typ"`
	AuthorizedParty   string `json:"azp"`
	PreferredUsername string `json:"preferred_username"`
	Email             string `json:"email"`
	Scope             string `json:"scope"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
	ResourceAccess map[string]struct {
		Roles []string `json:"roles"`
	} `json:"resource_access"`
}

// Authenticate verifies the token's signature, issuer, expiry and (if
// configured) audience. Every failure is an ErrKindUnauthenticated error with
// the reason kept as its cause for the logs.
func (a *Authenticator) Authenticate(ctx context.Context, rawToken string) (domain.Principal, error) {
	var c claims
	if _, err := a.parser.ParseWithClaims(rawToken, &c, a.keys.KeyfuncCtx(ctx)); err != nil {
		return domain.Principal{}, domain.WrapError(domain.ErrKindUnauthenticated, err, "invalid or expired token")
	}
	// Keycloak signs ID and refresh tokens with the same key and issuer;
	// only access tokens ("Bearer") may be used to call an API.
	if c.Type != "" && !strings.EqualFold(c.Type, "Bearer") {
		return domain.Principal{}, domain.WrapError(domain.ErrKindUnauthenticated,
			fmt.Errorf("token type is %q, not an access token", c.Type), "invalid or expired token")
	}
	if c.Subject == "" {
		return domain.Principal{}, domain.WrapError(domain.ErrKindUnauthenticated,
			errors.New("token has no sub claim"), "invalid or expired token")
	}

	// Realm roles as-is, client roles as "<client>:<role>".
	roles := slices.Clone(c.RealmAccess.Roles)
	for client, access := range c.ResourceAccess {
		for _, role := range access.Roles {
			roles = append(roles, client+":"+role)
		}
	}
	slices.Sort(roles)

	return domain.Principal{
		Subject:  c.Subject,
		Username: c.PreferredUsername,
		Email:    c.Email,
		ClientID: c.AuthorizedParty,
		Roles:    roles,
		Scopes:   strings.Fields(c.Scope),
	}, nil
}
