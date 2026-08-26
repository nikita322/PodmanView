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

// userFromCookie validates the session cookie and returns the corresponding
// user. Invalid cookies are cleared on the response. It returns nil if there
// is no valid session.
func (m *Middleware) userFromCookie(w http.ResponseWriter, r *http.Request) *User {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return nil
	}

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
		return nil
	}

	return &User{
		Username: claims.Username,
		UID:      claims.UID,
		Role:     claims.Role,
	}
}

// setUser stores the user in the request context and invokes next.
func setUser(r *http.Request, user *User, next http.Handler, w http.ResponseWriter) {
	ctx := context.WithValue(r.Context(), UserContextKey, user)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// RequireAuth middleware checks for valid JWT token
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := m.userFromCookie(w, r)
		if user == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		setUser(r, user, next, w)
	})
}

// RequireAuthOrToken authenticates the request using either a valid JWT
// session cookie or the configured static API token.
func (m *Middleware) RequireAuthOrToken(apiToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Try a valid session cookie first.
		if user := m.userFromCookie(w, r); user != nil {
			setUser(r, user, next, w)
			return
		}

		// 2. Try the static API token.
		if apiToken != "" && validAPIToken(r, apiToken) {
			setUser(r, &User{
				Username: "api_token",
				UID:      "0",
				Role:     RoleAdmin,
			}, next, w)
			return
		}

		// 3. Unauthorized.
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

// RequireAdminOrToken authenticates the request using either the configured
// static API token (always treated as admin) or a valid admin JWT session
// cookie. Non-admin sessions are rejected with 403 Forbidden.
func (m *Middleware) RequireAdminOrToken(apiToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. API token takes precedence and always means admin.
		if apiToken != "" && validAPIToken(r, apiToken) {
			setUser(r, &User{
				Username: "api_token",
				UID:      "0",
				Role:     RoleAdmin,
			}, next, w)
			return
		}

		// 2. Try a valid admin session cookie.
		if user := m.userFromCookie(w, r); user != nil {
			if !user.IsAdmin() {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			setUser(r, user, next, w)
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
