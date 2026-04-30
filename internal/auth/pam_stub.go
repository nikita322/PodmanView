//go:build !linux
// +build !linux

package auth

import (
	"fmt"
	"os/user"
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

// PAMAuth handles PAM authentication (stub for non-Linux platforms)
type PAMAuth struct {
	serviceName string
	adminGroups []string
}

// NewPAMAuth creates new PAM authenticator (stub for non-Linux platforms)
func NewPAMAuth() *PAMAuth {
	return &PAMAuth{
		serviceName: "login",
		adminGroups: []string{"wheel", "sudo", "root", "admin"},
	}
}

// Authenticate returns error on non-Linux platforms
func (p *PAMAuth) Authenticate(username, password string) (*User, error) {
	return nil, fmt.Errorf("PAM authentication is not supported on this platform (Linux only)")
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
