package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

type contextKey string

const (
	UserContextKey contextKey = "user"
	CookieName     string     = "podmanview_token"
)

// Middleware handles authentication for protected routes
type Middleware struct {
	jwtManager *JWTManager
}

// NewMiddleware creates new auth middleware
func NewMiddleware(jwtManager *JWTManager) *Middleware {
	return &Middleware{jwtManager: jwtManager}
}

// RequireAuth middleware checks for valid JWT token
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to get token from cookie
		cookie, err := r.Cookie(CookieName)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Validate token
		claims, err := m.jwtManager.ValidateToken(cookie.Value)
		if err != nil {
			// Clear invalid cookie
			http.SetCookie(w, &http.Cookie{
				Name:     CookieName,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
			})
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Add user to context
		user := &User{
			Username: claims.Username,
			UID:      claims.UID,
			Role:     claims.Role,
		}
		ctx := context.WithValue(r.Context(), UserContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuthOrToken authenticates the request using either a valid JWT
// session cookie or the configured static API token.
func (m *Middleware) RequireAuthOrToken(apiToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Try a valid session cookie first.
		if cookie, err := r.Cookie(CookieName); err == nil {
			if claims, err := m.jwtManager.ValidateToken(cookie.Value); err == nil {
				user := &User{
					Username: claims.Username,
					UID:      claims.UID,
					Role:     claims.Role,
				}
				ctx := context.WithValue(r.Context(), UserContextKey, user)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Invalid cookie: clear it, but still allow a valid API token below.
			http.SetCookie(w, &http.Cookie{
				Name:     CookieName,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
			})
		}

		// 2. Try the static API token.
		if apiToken != "" && validAPIToken(r, apiToken) {
			user := &User{
				Username: "api_token",
				UID:      "0",
				Role:     RoleAdmin,
			}
			ctx := context.WithValue(r.Context(), UserContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 3. Unauthorized.
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

// apiTokenFromRequest extracts a candidate API token from the request.
func apiTokenFromRequest(r *http.Request) string {
	if token := r.URL.Query().Get("api_token"); token != "" {
		return token
	}

	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	}

	if header := r.Header.Get("X-API-Token"); header != "" {
		return header
	}

	return ""
}

// validAPIToken reports whether the request carries the configured API token.
func validAPIToken(r *http.Request, apiToken string) bool {
	candidate := apiTokenFromRequest(r)
	if candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(apiToken)) == 1
}

// GetUserFromContext extracts user from request context
func GetUserFromContext(ctx context.Context) *User {
	user, ok := ctx.Value(UserContextKey).(*User)
	if !ok {
		return nil
	}
	return user
}

// SetUserContext adds user to context
func SetUserContext(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, UserContextKey, user)
}

// SetAuthCookie sets JWT token in HttpOnly cookie
// Automatically sets Secure flag when request is over HTTPS
func SetAuthCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	// Determine if request came over HTTPS (direct TLS or via reverse proxy)
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
	})
}

// ClearAuthCookie removes auth cookie
func ClearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}
