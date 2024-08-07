package util

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/carlmjohnson/requests"
	"github.com/go-chi/jwtauth/v5"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"golang.org/x/exp/slices"
)

type Body struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// a custom way of getting a JWT from an http.Request (this "custom way" being
// to log in to supabase using the basic auth creds included in the req). For
// use below in BasicAuthJwtVerifier, plugs into jwtauth.Verify
func GetTokenFromBasicAuth(supaUrl, supaAnonKey string) func(req *http.Request) string {
	return func(req *http.Request) string {

		email, password, ok := req.BasicAuth()
		if !ok {
			log.Printf("Unable to parse basic auth credentials")
			return ""
		}

		body := Body{
			Email:    email,
			Password: password,
		}

		var user map[string]interface{}

		err := requests.
			URL(supaUrl).
			Path("/auth/v1/token").
			Param("grant_type", "password").
			Header("apikey", supaAnonKey).
			// no authorization header since this is the anon / signin request
			BodyJSON(&body).
			ToJSON(&user).
			Fetch(req.Context())

		if err != nil {
			log.Printf("Error authing with supabase: %v", err)
			return ""
		}

		// this is a hack! It's the only way of keeping this token (the encoded string,
		// not the Token object) around so we can use it in supabase calls later
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", user["access_token"].(string)))

		return user["access_token"].(string)
	}
}

// derived from https://github.com/go-chi/jwtauth/blob/master/jwtauth.go
//
// like jwtauth.Verifier, but gets the token from logging in to Supabase with
// basic auth credentials instead of scanning headers/cookies for "Bearer $token"
func BasicAuthJwtVerifier(ja *jwtauth.JWTAuth, supaUrl, supaAnonKey string) func(http.Handler) http.Handler {
	return jwtauth.Verify(ja, GetTokenFromBasicAuth(supaUrl, supaAnonKey))
}

// derived from https://github.com/go-chi/jwtauth/blob/master/jwtauth.go
//
// permissive -- don't check to see if the JWT "Verifier" middleware worked or errored
// basic -- on error, additionally sets a header which requests HTTP basic auth to be set
func CheckJwtMiddleware(permissive, basic bool) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(out http.ResponseWriter, req *http.Request) {

			if permissive {

				log.Printf("Permissive mode is enabled, not validating JWT tokens! SHOULD NOT SEE IN PROD")

			} else {

				token, _, err := jwtauth.FromContext(req.Context())

				if err != nil {
					if basic {
						out.Header().Add("WWW-Authenticate", "Basic")
					}
					http.Error(out, fmt.Sprintf("Unable to parse claims from JWT: %v", err), http.StatusUnauthorized)
					return
				}

				if (token == nil) || (jwt.Validate(token) != nil) {
					if basic {
						out.Header().Add("WWW-Authenticate", "Basic")
					}
					http.Error(out, fmt.Sprintf("Unable to validate JWT token"), http.StatusUnauthorized)
					return
				}
			}

			// token is authenticated / we've decided we don't care, pass it through
			next.ServeHTTP(out, req)
			return
		})
	}
}

// gets the desired pull zone ID from the query params, then checks the JWT metadata
// to make sure the user is allowed to query that zone
func CheckZoneIdMiddleware(permissive bool) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(out http.ResponseWriter, req *http.Request) {

			if permissive {

				log.Printf("Permissive mode is enabled, not validating JWT tokens! SHOULD NOT SEE IN PROD")

			} else {

				// already checked for errors etc in the previous CheckJwtMiddleware
				_, claims, _ := jwtauth.FromContext(req.Context())

				zoneId := req.URL.Query().Get("zoneid")
				if zoneId == "" {
					http.Error(out, "Query param 'zoneid' not provided, quitting", http.StatusBadRequest)
					return
				}

				zoneIdInt, err := strconv.Atoi(zoneId)
				if err != nil {
					http.Error(out, "Query param 'zoneid' could not be parsed as int, quitting", http.StatusBadRequest)
					return
				}

				existingZoneIds, err := GetZoneIdsFromClaims(claims)
				if err != nil {
					http.Error(out, fmt.Sprintf("Unable to get zone IDs from JWT claims: %v", err), http.StatusBadRequest)
					return
				}

				if !slices.Contains(existingZoneIds, zoneIdInt) {
					http.Error(out, fmt.Sprintf("User not authorized to query zone ID %v", zoneIdInt), http.StatusUnauthorized)
					return
				}
			}

			// user is allowed to query this pull zone, pass it through
			next.ServeHTTP(out, req)
			return
		})
	}
}

// similar to the above, but enforces "readonly" field in app_metadata to disallow
// the creation or updates of sites
func CheckReadOnlyMiddleware(permissive bool) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(out http.ResponseWriter, req *http.Request) {

			if permissive {

				log.Printf("Permissive mode is enabled, not validating JWT tokens! SHOULD NOT SEE IN PROD")

			} else {

				// already checked for errors etc in the previous CheckJwtMiddleware
				_, claims, _ := jwtauth.FromContext(req.Context())

				readonly, err := GetReadonlyFromClaims(claims)
				if err != nil {
					http.Error(out, fmt.Sprintf("Unable to get readonly from JWT claims: %v", err), http.StatusBadRequest)
					return
				}

				if readonly {
					http.Error(out, fmt.Sprintf("User's account is readonly"), http.StatusUnauthorized)
					return
				}
			}

			// user account is not readonly, pass it through
			next.ServeHTTP(out, req)
			return
		})
	}
}
