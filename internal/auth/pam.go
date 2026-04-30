package auth

import (
	"fmt"
	"os/user"

	"github.com/msteinert/pam"
)

// Role represents user access level
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleReadOnly Role = "readonly"
)

// User represents authenticated user
type User struct {
	Username string `json:"username"`
	UID      string `json:"uid"`
	Role     Role   `json:"role"`
}

// PAMAuth handles PAM authentication
type PAMAuth struct {
	serviceName string
	adminGroups []string
}

// NewPAMAuth creates new PAM authenticator
func NewPAMAuth() *PAMAuth {
	return &PAMAuth{
		serviceName: "login",
		adminGroups: []string{"wheel", "sudo", "root", "admin"},
	}
}

// Authenticate verifies username and password via PAM
func (p *PAMAuth) Authenticate(username, password string) (*User, error) {
	// Create PAM transaction
	t, err := pam.StartFunc(p.serviceName, username, func(s pam.Style, msg string) (string, error) {
		switch s {
		case pam.PromptEchoOff:
			return password, nil
		case pam.PromptEchoOn:
			return username, nil
		case pam.ErrorMsg:
			return "", fmt.Errorf("PAM error: %s", msg)
		case pam.TextInfo:
			return "", nil
		}
		return "", fmt.Errorf("unrecognized PAM message style: %v", s)
	})
	if err != nil {
		return nil, fmt.Errorf("PAM start failed: %w", err)
	}

	// Authenticate
	if err := t.Authenticate(0); err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	// Validate account
	if err := t.AcctMgmt(0); err != nil {
		return nil, fmt.Errorf("account validation failed: %w", err)
	}

	// Get user info
	u, err := user.Lookup(username)
	if err != nil {
		return nil, fmt.Errorf("user lookup failed: %w", err)
	}

	// Determine role based on group membership
	role := p.determineRole(username)

	return &User{
		Username: username,
		UID:      u.Uid,
		Role:     role,
	}, nil
}

// determineRole checks if user is admin based on group membership
func (p *PAMAuth) determineRole(username string) Role {
	u, err := user.Lookup(username)
	if err != nil {
		return RoleReadOnly
	}

	// Get user's groups
	groups, err := u.GroupIds()
	if err != nil {
		return RoleReadOnly
	}

	// Check if user is in any admin group
	for _, gid := range groups {
		group, err := user.LookupGroupId(gid)
		if err != nil {
			continue
		}

		for _, adminGroup := range p.adminGroups {
			if group.Name == adminGroup {
				return RoleAdmin
			}
		}
	}

	// Also check if username is root
	if username == "root" {
		return RoleAdmin
	}

	return RoleReadOnly
}

// IsAdmin checks if user has admin role
func (u *User) IsAdmin() bool {
	return u.Role == RoleAdmin
}
