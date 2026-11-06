package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

func sessionOwnerFile(configHome, session string) (string, error) {
	if err := checkSessionName(session); err != nil {
		return "", err
	}
	base := filepath.Join(configHome, "mate", "session-owners")
	p := filepath.Clean(filepath.Join(base, session))
	rel, err := filepath.Rel(base, p)
	if err != nil || rel != session {
		return "", fmt.Errorf("session owner path escapes mate/session-owners")
	}
	return p, nil
}

// SessionOwner is the workspace id the owner marker of a session names, and
// whether there is a marker at all.
func SessionOwner(configHome, session string) (owner string, found bool, err error) {
	p, err := sessionOwnerFile(configHome, session)
	if err != nil {
		return "", false, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, observability.WrapError(observability.CodeUnknown, "session owner marker", err)
	}
	return strings.TrimSpace(string(raw)), true, nil
}

// ReclaimSessionOwner makes workspaceID the owner of a session whatever the
// marker said. The caller has established that the recorded owner no longer
// exists; ClaimSessionOwner never does this itself, because two workspaces
// silently sharing a session is what the marker is there to refuse.
func ReclaimSessionOwner(configHome, session, workspaceID string) error {
	p, err := sessionOwnerFile(configHome, session)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return observability.WrapError(observability.CodeUnknown, "session owner marker", err)
	}
	return ClaimSessionOwner(configHome, session, workspaceID)
}

// ClaimSessionOwner records which workspace id owns a Herdr session name in
// the config home, with O_EXCL so two processes cannot silently share the
// address. persistence.NameRegistry refuses this scope (ADR 0004); this file
// is the cross-workspace record. Same-owner re-claim is idempotent.
func ClaimSessionOwner(configHome, session, workspaceID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return observability.NewError(observability.CodeUsage, "session owner marker requires a workspace id")
	}
	if strings.TrimSpace(configHome) == "" {
		return observability.NewError(observability.CodeUsage, "session owner marker requires config home")
	}
	p, err := sessionOwnerFile(configHome, session)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return observability.WrapError(observability.CodePermission, "session owner directory", err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		_, werr := f.WriteString(workspaceID + "\n")
		cerr := f.Close()
		if werr != nil {
			return observability.WrapError(observability.CodeUnknown, "session owner marker", werr)
		}
		if cerr != nil {
			return observability.WrapError(observability.CodeUnknown, "session owner marker", cerr)
		}
		return nil
	}
	if !errors.Is(err, os.ErrExist) {
		return observability.WrapError(observability.CodeUnknown, "session owner marker", err)
	}
	raw, rerr := os.ReadFile(p)
	if rerr != nil {
		return observability.WrapError(observability.CodeUnknown, "session owner marker", rerr)
	}
	if strings.TrimSpace(string(raw)) == workspaceID {
		return nil
	}
	return observability.NewError(
		observability.CodeAlreadyExists,
		fmt.Sprintf("herdr session %q is owned by workspace %s, not %s", session, strings.TrimSpace(string(raw)), workspaceID),
	)
}
